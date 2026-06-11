import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
  type RefObject,
} from "react";
import { flushSync } from "react-dom";

import type { BoardColumn, WorkflowBoard } from "../../api";
import { runViewTransition } from "../../app/viewTransitions";
import { chromeContentPaddingClassName } from "../../ui/chromePadding";
import { BoardCardMotionContext, type BoardCardMotionContextValue } from "./BoardCardMotionContext";
import { KanbanColumn, KanbanGroup } from "./BoardColumns";
import {
  boardCardMotionParticipants,
  boardCardColumnIDsWithCards,
  boardCardSnapshotsEqual,
  boardCardSnapshotFromEntries,
  boardRailLayoutSignature,
  cardBelongsToColumn,
  dirtyBoardCardColumnIDs,
  type BoardCardColumnsSnapshot,
} from "./BoardCardMotionModel";
import { toKanbanCardVM, toKanbanColumnVM, toKanbanGroupVM, type KanbanCardVM } from "./BoardColumnViewModel";
import type { BoardCardDragPayload, BoardColumnDropState } from "./BoardDragTypes";
import { boardSections } from "./BoardModel";
import { useBoardNodeCards } from "./useBoardData";
import { useColumnVisibility } from "./useColumnVisibility";

type BoardColumnQuerySnapshot = Readonly<{
  cards: readonly KanbanCardVM[];
  generation: number;
  isFetching: boolean;
  isSettled: boolean;
}>;

type BoardMotionPhase = "idle" | "arming" | "running";

type ArmedTransition = Readonly<{
  attemptID: number;
  layoutSignature: string;
  runtimeGeneration: number;
  namesByCardID: ReadonlyMap<string, string>;
  nextDisplayed: BoardCardColumnsSnapshot;
  revealCardIDs: ReadonlySet<string>;
}>;

type DisplayedSnapshot = Readonly<{
  layoutSignature: string;
  columns: BoardCardColumnsSnapshot;
}>;

export type BoardRailMotionControllerProps = Readonly<{
  actionsDisabled: boolean;
  board: WorkflowBoard;
  columnDropState: (column: BoardColumn) => BoardColumnDropState;
  columnIsCollapsed: (column: BoardColumn) => boolean;
  firstActiveID: string | undefined;
  onCardClick: (taskID: string) => void;
  onCardDragEnd: () => void;
  onCardDragStart: (payload: BoardCardDragPayload) => void;
  onCardsLoadError: (error: unknown) => void;
  onDeleteTask: (taskID: string) => void;
  onDropTask: (event: DragEvent<HTMLElement>, column: BoardColumn) => void;
  onExpandColumn: (columnID: string) => void;
  onInterruptTask: (taskID: string, runID: string) => void;
  onResumeTask: (taskID: string, runID: string) => void;
  scrollportRef: RefObject<HTMLDivElement | null>;
}>;

const staleSnapshotTimeoutMs = 900;
const emptyColumnsSnapshot: BoardCardColumnsSnapshot = new Map();

export function BoardRailMotionController({
  actionsDisabled,
  board,
  columnDropState,
  columnIsCollapsed,
  firstActiveID,
  onCardClick,
  onCardDragEnd,
  onCardDragStart,
  onCardsLoadError,
  onDeleteTask,
  onDropTask,
  onExpandColumn,
  onInterruptTask,
  onResumeTask,
  scrollportRef,
}: BoardRailMotionControllerProps) {
  const sections = useMemo(() => boardSections(board), [board]);
  const layoutSignature = useMemo(() => boardRailLayoutSignature(board, sections, firstActiveID), [board, firstActiveID, sections]);
  const [displayedSnapshot, setDisplayedSnapshot] = useState<DisplayedSnapshot>(() => ({
    columns: new Map(),
    layoutSignature,
  }));
  const [activeNamesByCardID, setActiveNamesByCardID] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [revealCardIDs, setRevealCardIDs] = useState<ReadonlySet<string>>(() => new Set());
  const [armedTransition, setArmedTransition] = useState<ArmedTransition | null>(null);
  const [heldExpandedColumnIDs, setHeldExpandedColumnIDs] = useState<ReadonlySet<string>>(() => new Set());
  const [columnVersion, setColumnVersion] = useState(0);
  const displayedColumns = displayedSnapshot.layoutSignature === layoutSignature ? displayedSnapshot.columns : emptyColumnsSnapshot;
  const latestColumnsRef = useRef<ReadonlyMap<string, BoardColumnQuerySnapshot>>(new Map());
  const displayedColumnsRef = useRef(displayedColumns);
  const visibleCardIDsRef = useRef<ReadonlySet<string>>(new Set());
  const cardElementsRef = useRef<ReadonlyMap<string, HTMLElement>>(new Map());
  const cardObserverRef = useRef<IntersectionObserver | null>(null);
  const phaseRef = useRef<BoardMotionPhase>("idle");
  const followUpPendingRef = useRef(false);
  const attemptIDRef = useRef(0);
  const runtimeGenerationRef = useRef(0);
  const startedAttemptIDsRef = useRef<ReadonlySet<number>>(new Set());
  const timeoutRef = useRef<number | null>(null);
  const revealTimeoutsRef = useRef<ReadonlySet<number>>(new Set());
  const staleTimeoutDueRef = useRef(false);
  const layoutSignatureRef = useRef(layoutSignature);

  const latestSnapshot = useCallback(
    (): BoardCardColumnsSnapshot =>
      boardCardSnapshotFromEntries(
        Array.from(latestColumnsRef.current, ([columnID, snapshot]) => [columnID, snapshot.cards]),
      ),
    [],
  );

  const scheduleNextTransition = useCallback(
    (fromTimeout: boolean): void => {
      const nextDisplayed = latestSnapshot();
      const currentDisplayed = displayedColumnsRef.current;
      if (boardCardSnapshotsEqual(currentDisplayed, nextDisplayed)) {
        clearStaleSnapshotTimer(timeoutRef);
        return;
      }
      const dirtyColumns = dirtyBoardCardColumnIDs(currentDisplayed, nextDisplayed);
      const dirtySettled = dirtyColumns.every((columnID) => latestColumnsRef.current.get(columnID)?.isSettled ?? true);
      if (!fromTimeout && !dirtySettled) {
        clearStaleSnapshotTimer(timeoutRef);
        timeoutRef.current = window.setTimeout(() => {
          timeoutRef.current = null;
          staleTimeoutDueRef.current = true;
          setColumnVersion((version) => version + 1);
        }, staleSnapshotTimeoutMs);
        return;
      }
      clearStaleSnapshotTimer(timeoutRef);
      const participants = boardCardMotionParticipants(currentDisplayed, nextDisplayed, visibleCardIDsRef.current);
      if (participants.namesByCardID.size === 0) {
        displayedColumnsRef.current = nextDisplayed;
        setDisplayedSnapshot({ columns: nextDisplayed, layoutSignature });
        setRevealCardIDs(participants.revealCardIDs);
        scheduleRevealClear(revealTimeoutsRef, participants.revealCardIDs, setRevealCardIDs);
        return;
      }
      const attemptID = attemptIDRef.current + 1;
      attemptIDRef.current = attemptID;
      phaseRef.current = "arming";
      setHeldExpandedColumnIDs(boardCardColumnIDsWithCards(currentDisplayed));
      setActiveNamesByCardID(participants.namesByCardID);
      setArmedTransition({
        attemptID,
        layoutSignature,
        runtimeGeneration: runtimeGenerationRef.current,
        namesByCardID: participants.namesByCardID,
        nextDisplayed,
        revealCardIDs: participants.revealCardIDs,
      });
    },
    [latestSnapshot, layoutSignature],
  );

  useLayoutEffect(() => {
    if (layoutSignatureRef.current !== layoutSignature) {
      layoutSignatureRef.current = layoutSignature;
      runtimeGenerationRef.current += 1;
      attemptIDRef.current += 1;
      clearStaleSnapshotTimer(timeoutRef);
      clearRevealTimers(revealTimeoutsRef);
      staleTimeoutDueRef.current = false;
      phaseRef.current = "idle";
      followUpPendingRef.current = false;
      latestColumnsRef.current = new Map();
      setActiveNamesByCardID(new Map());
      setRevealCardIDs(new Set());
      setHeldExpandedColumnIDs(new Set());
      setArmedTransition(null);
      const nextDisplayed = new Map<string, readonly KanbanCardVM[]>();
      displayedColumnsRef.current = nextDisplayed;
      setDisplayedSnapshot({ columns: nextDisplayed, layoutSignature });
    }
  }, [latestSnapshot, layoutSignature]);

  useEffect(() => {
    if (displayedSnapshot.layoutSignature === layoutSignature) {
      displayedColumnsRef.current = displayedSnapshot.columns;
    }
  }, [displayedSnapshot, layoutSignature]);

  useLayoutEffect(() => {
    return () => {
      runtimeGenerationRef.current += 1;
      attemptIDRef.current += 1;
      clearStaleSnapshotTimer(timeoutRef);
      clearRevealTimers(revealTimeoutsRef);
      staleTimeoutDueRef.current = false;
      cardObserverRef.current?.disconnect();
      cardObserverRef.current = null;
    };
  }, []);

  const reportColumnSnapshot = useCallback((columnID: string, snapshot: BoardColumnQuerySnapshot): void => {
    const current = latestColumnsRef.current;
    const previous = current.get(columnID);
    if (previous !== undefined && previous.generation > snapshot.generation) {
      return;
    }
    latestColumnsRef.current = new Map(current).set(columnID, snapshot);
    if (!displayedColumnsRef.current.has(columnID)) {
      const nextDisplayed = new Map(displayedColumnsRef.current).set(columnID, snapshot.cards);
      displayedColumnsRef.current = nextDisplayed;
      setDisplayedSnapshot({ columns: nextDisplayed, layoutSignature });
    }
    setColumnVersion((version) => version + 1);
  }, [layoutSignature]);

  useEffect(() => {
    if (columnVersion === 0 || phaseRef.current !== "idle") {
      if (phaseRef.current === "arming" || phaseRef.current === "running") {
        followUpPendingRef.current = true;
      }
      return;
    }
    const fromTimeout = staleTimeoutDueRef.current;
    staleTimeoutDueRef.current = false;
    scheduleNextTransition(fromTimeout);
  }, [columnVersion, scheduleNextTransition]);

  useLayoutEffect(() => {
    if (armedTransition === null) {
      return;
    }
    const startedAttemptIDs = startedAttemptIDsRef.current;
    if (startedAttemptIDs.has(armedTransition.attemptID)) {
      return;
    }
    startedAttemptIDsRef.current = new Set(startedAttemptIDs).add(armedTransition.attemptID);
    queueMicrotask(() => {
      if (
        armedTransition.attemptID !== attemptIDRef.current ||
        armedTransition.layoutSignature !== layoutSignatureRef.current ||
        armedTransition.runtimeGeneration !== runtimeGenerationRef.current
      ) {
        return;
      }
      phaseRef.current = "running";
      void runViewTransition({
        scope: "board-card",
        update: () => {
          flushSync(() => {
            displayedColumnsRef.current = armedTransition.nextDisplayed;
            setDisplayedSnapshot({
              columns: armedTransition.nextDisplayed,
              layoutSignature: armedTransition.layoutSignature,
            });
            setRevealCardIDs(armedTransition.revealCardIDs);
          });
        },
      }).then((transition) => {
        void transition.finished.finally(() => {
          if (
            armedTransition.attemptID !== attemptIDRef.current ||
            armedTransition.layoutSignature !== layoutSignatureRef.current ||
            armedTransition.runtimeGeneration !== runtimeGenerationRef.current
          ) {
            return;
          }
          phaseRef.current = "idle";
          setArmedTransition(null);
          setActiveNamesByCardID(new Map());
          setHeldExpandedColumnIDs(new Set());
          scheduleRevealClear(revealTimeoutsRef, armedTransition.revealCardIDs, setRevealCardIDs);
          if (followUpPendingRef.current) {
            followUpPendingRef.current = false;
            scheduleNextTransition(false);
          }
        });
      });
    });
  }, [armedTransition, scheduleNextTransition]);

  const registerCard = useCallback((cardID: string, element: HTMLElement | null) => {
    registerCardElement({ cardElementsRef, cardObserverRef, visibleCardIDsRef }, cardID, element);
  }, []);

  const motionContext = useMemo<BoardCardMotionContextValue>(
    () => ({
      cardClassName(cardID) {
        return revealCardIDs.has(cardID) ? "board-card-enter-reveal" : undefined;
      },
      cardStyle(cardID) {
        const transitionName = activeNamesByCardID.get(cardID);
        return transitionName === undefined ? undefined : { viewTransitionName: transitionName };
      },
      registerCard,
    }),
    [activeNamesByCardID, registerCard, revealCardIDs],
  );

  function effectiveColumnIsCollapsed(column: BoardColumn): boolean {
    return columnIsCollapsed(column) && !heldExpandedColumnIDs.has(column.id);
  }

  return (
    <BoardCardMotionContext.Provider value={motionContext}>
      <div
        className={`flex h-full min-h-0 w-max min-w-full gap-[var(--space-2)] ${chromeContentPaddingClassName}`}
        data-testid="board-column-rail"
      >
        {sections.map((section) =>
          section.kind === "group" ? (
            <KanbanGroup
              group={toKanbanGroupVM(section.group)}
              hideHeader={section.columns.every(effectiveColumnIsCollapsed)}
              key={section.id}
            >
              {section.columns.map((column) => (
                <BoardColumnMotionBoundary
                  actionsDisabled={actionsDisabled}
                  board={board}
                  displayedCards={displayedColumns.get(column.id)}
                  column={column}
                  dropState={columnDropState(column)}
                  isCollapsed={effectiveColumnIsCollapsed(column)}
                  isFirstActive={column.id === firstActiveID}
                  key={`${board.projectID}:${board.selectedWorkflow.id}:${column.id}`}
                  latestIsCollapsed={columnIsCollapsed(column)}
                  onCardClick={onCardClick}
                  onCardDragEnd={onCardDragEnd}
                  onCardDragStart={onCardDragStart}
                  onCardsLoadError={onCardsLoadError}
                  onDeleteTask={onDeleteTask}
                  onDropTask={onDropTask}
                  onExpandColumn={onExpandColumn}
                  onInterruptTask={onInterruptTask}
                  onReportColumnSnapshot={reportColumnSnapshot}
                  onResumeTask={onResumeTask}
                  scrollportRef={scrollportRef}
                />
              ))}
            </KanbanGroup>
          ) : (
            <BoardColumnMotionBoundary
              actionsDisabled={actionsDisabled}
              board={board}
              displayedCards={displayedColumns.get(section.column.id)}
              column={section.column}
              dropState={columnDropState(section.column)}
              isCollapsed={effectiveColumnIsCollapsed(section.column)}
              isFirstActive={section.column.id === firstActiveID}
              key={`${board.projectID}:${board.selectedWorkflow.id}:${section.id}`}
              latestIsCollapsed={columnIsCollapsed(section.column)}
              onCardClick={onCardClick}
              onCardDragEnd={onCardDragEnd}
              onCardDragStart={onCardDragStart}
              onCardsLoadError={onCardsLoadError}
              onDeleteTask={onDeleteTask}
              onDropTask={onDropTask}
              onExpandColumn={onExpandColumn}
              onInterruptTask={onInterruptTask}
              onReportColumnSnapshot={reportColumnSnapshot}
              onResumeTask={onResumeTask}
              scrollportRef={scrollportRef}
            />
          ),
        )}
      </div>
    </BoardCardMotionContext.Provider>
  );
}

function BoardColumnMotionBoundary({
  actionsDisabled,
  board,
  displayedCards,
  column,
  dropState,
  isCollapsed,
  isFirstActive,
  latestIsCollapsed,
  onCardClick,
  onCardDragEnd,
  onCardDragStart,
  onCardsLoadError,
  onDeleteTask,
  onDropTask,
  onExpandColumn,
  onInterruptTask,
  onReportColumnSnapshot,
  onResumeTask,
  scrollportRef,
}: Readonly<{
  actionsDisabled: boolean;
  board: WorkflowBoard;
  displayedCards: readonly KanbanCardVM[] | undefined;
  column: BoardColumn;
  dropState: BoardColumnDropState;
  isCollapsed: boolean;
  isFirstActive: boolean;
  latestIsCollapsed: boolean;
  onCardClick: (taskID: string) => void;
  onCardDragEnd: () => void;
  onCardDragStart: (payload: BoardCardDragPayload) => void;
  onCardsLoadError: (error: unknown) => void;
  onDeleteTask: (taskID: string) => void;
  onDropTask: (event: DragEvent<HTMLElement>, column: BoardColumn) => void;
  onExpandColumn: (columnID: string) => void;
  onInterruptTask: (taskID: string, runID: string) => void;
  onReportColumnSnapshot: (columnID: string, snapshot: BoardColumnQuerySnapshot) => void;
  onResumeTask: (taskID: string, runID: string) => void;
  scrollportRef: RefObject<HTMLDivElement | null>;
}>) {
  const [columnElement, setColumnElement] = useState<HTMLElement | null>(null);
  const isVisible = useColumnVisibility(scrollportRef, columnElement);
  const queryEnabled = isVisible && !latestIsCollapsed;
  const cardsQuery = useBoardNodeCards(board.projectID, board.selectedWorkflow.id, column.id, queryEnabled);
  const generationRef = useRef(0);
  const queryCards = useMemo(
    () => cardsQuery.data?.pages.flatMap((page) => page.cards) ?? [],
    [cardsQuery.data?.pages],
  );
  const cardVMs = useMemo(
    () => queryCards.map(toKanbanCardVM).filter((card) => cardBelongsToColumn(column, card)),
    [column, queryCards],
  );
  const renderedCards = displayedCards ?? cardVMs;
  const columnVM = useMemo(() => toKanbanColumnVM(column), [column]);

  useEffect(() => {
    if (cardsQuery.isError) {
      onCardsLoadError(cardsQuery.error);
    }
  }, [cardsQuery.error, cardsQuery.isError, onCardsLoadError]);

  useEffect(() => {
    generationRef.current += 1;
    onReportColumnSnapshot(column.id, {
      cards: cardVMs,
      generation: generationRef.current,
      isFetching: cardsQuery.isFetching,
      isSettled: !queryEnabled || (!cardsQuery.isPending && !cardsQuery.isFetching),
    });
  }, [cardVMs, cardsQuery.isFetching, cardsQuery.isPending, column.id, onReportColumnSnapshot, queryEnabled]);

  return (
    <KanbanColumn
      actionsDisabled={actionsDisabled}
      cards={renderedCards}
      column={columnVM}
      columnRef={setColumnElement}
      dropState={dropState}
      hasMoreCards={cardsQuery.hasNextPage}
      isCollapsed={isCollapsed}
      isFirstActive={isFirstActive}
      isLoadingMoreCards={(queryEnabled && cardsQuery.isPending) || cardsQuery.isFetchingNextPage}
      onCardClick={onCardClick}
      onCardDragEnd={onCardDragEnd}
      onCardDragStart={onCardDragStart}
      onDeleteTask={onDeleteTask}
      onDropTask={(event) => {
        onDropTask(event, column);
      }}
      onExpandColumn={() => {
        onExpandColumn(column.id);
      }}
      onInterruptTask={onInterruptTask}
      onLoadMoreCards={() => {
        if (cardsQuery.hasNextPage && !cardsQuery.isFetchingNextPage) {
          void cardsQuery.fetchNextPage();
        }
      }}
      onResumeTask={onResumeTask}
    />
  );
}

function clearStaleSnapshotTimer(timeoutRef: { current: number | null }): void {
  if (timeoutRef.current === null) {
    return;
  }
  window.clearTimeout(timeoutRef.current);
  timeoutRef.current = null;
}

function scheduleRevealClear(
  revealTimeoutsRef: { current: ReadonlySet<number> },
  revealCardIDs: ReadonlySet<string>,
  setRevealCardIDs: (update: (current: ReadonlySet<string>) => ReadonlySet<string>) => void,
): void {
  const timeout = window.setTimeout(() => {
    revealTimeoutsRef.current = withoutTimeout(revealTimeoutsRef.current, timeout);
    setRevealCardIDs((current) => (current === revealCardIDs ? new Set() : current));
  }, 420);
  revealTimeoutsRef.current = new Set(revealTimeoutsRef.current).add(timeout);
}

function clearRevealTimers(revealTimeoutsRef: { current: ReadonlySet<number> }): void {
  for (const timeout of revealTimeoutsRef.current) {
    window.clearTimeout(timeout);
  }
  revealTimeoutsRef.current = new Set();
}

function withoutTimeout(timeouts: ReadonlySet<number>, timeout: number): ReadonlySet<number> {
  const next = new Set(timeouts);
  next.delete(timeout);
  return next;
}

type BoardCardVisibilityRegistry = Readonly<{
  cardElementsRef: { current: ReadonlyMap<string, HTMLElement> },
  cardObserverRef: { current: IntersectionObserver | null },
  visibleCardIDsRef: { current: ReadonlySet<string> },
}>;

function registerCardElement(
  registry: BoardCardVisibilityRegistry,
  cardID: string,
  element: HTMLElement | null,
): void {
  const { cardElementsRef, cardObserverRef, visibleCardIDsRef } = registry;
  const currentElement = cardElementsRef.current.get(cardID);
  if (currentElement !== undefined) {
    cardObserverRef.current?.unobserve(currentElement);
  }

  const nextElements = new Map(cardElementsRef.current);
  if (element === null) {
    nextElements.delete(cardID);
    visibleCardIDsRef.current = nextVisibleCardIDs(visibleCardIDsRef.current, cardID, false);
    cardElementsRef.current = nextElements;
    return;
  }

  element.dataset.boardCardMotionId = cardID;
  nextElements.set(cardID, element);
  cardElementsRef.current = nextElements;

  if (typeof IntersectionObserver === "undefined") {
    visibleCardIDsRef.current = nextVisibleCardIDs(visibleCardIDsRef.current, cardID, true);
    return;
  }

  cardObserverRef.current ??= new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!(entry.target instanceof HTMLElement)) {
          continue;
        }
        const observedCardID = entry.target.dataset.boardCardMotionId;
        if (observedCardID === undefined) {
          continue;
        }
        visibleCardIDsRef.current = nextVisibleCardIDs(
          visibleCardIDsRef.current,
          observedCardID,
          entry.isIntersecting,
        );
      }
    });
  cardObserverRef.current.observe(element);
}

function nextVisibleCardIDs(current: ReadonlySet<string>, cardID: string, visible: boolean): ReadonlySet<string> {
  const next = new Set(current);
  if (visible) {
    next.add(cardID);
  } else {
    next.delete(cardID);
  }
  return next;
}
