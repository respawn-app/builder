import type { DragEvent } from "react";
import { useMemo, useRef, useState, type FocusEvent } from "react";
import { useTranslation } from "react-i18next";

import type { BoardColumn, WorkflowBoard, WorkflowPickerItem } from "../../api";
import { errorMessage } from "../../api/errors";
import { useAppNavigation } from "../../app/navigation";
import { useConnectionSnapshot } from "../../app/useConnectionSnapshot";
import { Badge, EmptyState, ErrorState } from "../../ui";
import { TaskDetailDialog } from "../task-detail/TaskDetailDialog";
import { useOpenTaskDetail } from "../task-detail/useOpenTaskDetail";
import { NewTaskDialog } from "../tasks/NewTaskDialog";
import { KanbanColumn, KanbanGroup } from "./BoardColumns";
import { boardSections, cardsForColumn } from "./BoardModel";
import { useBoard, useBoardTaskActions, useProjectBoardSubscription } from "./useBoardData";

export type BoardRouteProps = Readonly<{
  projectId: string;
  workflowId: string;
  selectedTaskId: string;
  resumeRunId: string;
}>;

export function BoardRoute({ projectId, workflowId, selectedTaskId, resumeRunId }: BoardRouteProps) {
  const { t } = useTranslation();
  const boardQuery = useBoard(projectId, workflowId);
  const board = boardQuery.data;
  useProjectBoardSubscription(
    projectId,
    workflowId,
    board?.selectedWorkflow.id ?? workflowId,
    board?.latestEventSequence ?? 0,
  );

  if (boardQuery.isPending) {
    return <p>{t("states.loading")}</p>;
  }
  if (boardQuery.isError) {
    return (
      <ErrorState
        body={errorMessage(boardQuery.error)}
        onRetry={() => void boardQuery.refetch()}
        retryLabel={t("app.retry")}
        title={t("states.error")}
      />
    );
  }
  if (board === undefined || board.workflows.length === 0) {
    return <EmptyState body={t("board.noWorkflowBody")} title={t("board.noWorkflowTitle")} />;
  }

  return (
    <BoardContent
      board={board}
      boardQueryWorkflowId={workflowId}
      hasMoreCards={boardQuery.hasNextPage}
      isLoadingMoreCards={boardQuery.isFetchingNextPage}
      onLoadMoreCards={() => void boardQuery.fetchNextPage()}
      resumeRunId={resumeRunId}
      selectedTaskId={selectedTaskId}
    />
  );
}

function BoardContent({
  board,
  boardQueryWorkflowId,
  hasMoreCards,
  isLoadingMoreCards,
  onLoadMoreCards,
  selectedTaskId,
  resumeRunId,
}: Readonly<{
  board: WorkflowBoard;
  boardQueryWorkflowId: string;
  hasMoreCards: boolean;
  isLoadingMoreCards: boolean;
  onLoadMoreCards: () => void;
  selectedTaskId: string;
  resumeRunId: string;
}>) {
  const { t } = useTranslation();
  const [newTaskOpen, setNewTaskOpen] = useState(false);
  const [doneExpanded, setDoneExpanded] = useState(false);
  const navigation = useAppNavigation();
  const openTaskDetail = useOpenTaskDetail();
  const connection = useConnectionSnapshot();
  const actions = useBoardTaskActions(board.projectID, boardQueryWorkflowId, board.selectedWorkflow.id);
  const actionsDisabled = connection.phase !== "connected";
  const activeColumns = useMemo(
    () => board.columns.filter((column) => !column.isBacklog && !column.isDone),
    [board.columns],
  );
  const sections = useMemo(() => boardSections(board), [board]);
  const firstActive = activeColumns[0];

  function dropTask(event: DragEvent<HTMLElement>, column: BoardColumn): void {
    event.preventDefault();
    const taskID = event.dataTransfer.getData("text/task-id");
    const card = board.cards.find((candidate) => candidate.id === taskID);
    if (taskID.length === 0 || connection.phase !== "connected" || !board.selectedWorkflow.validForTaskCreation) {
      return;
    }
    if (card?.actions.canStart === true && column.id === firstActive?.id) {
      void actions.start.mutateAsync(taskID);
      return;
    }
    if (card?.actions.manualMoveTargetNodeIDs.includes(column.id) === true) {
      void actions.move.mutateAsync({ taskID, targetNodeID: column.id });
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)] gap-[var(--space-4)]">
      <header className="flex flex-wrap items-start justify-between gap-[var(--space-4)]">
        <div>
          <p className="m-0 text-[0.72rem] font-extrabold uppercase tracking-[0.16em] text-[var(--color-secondary)]">
            {board.projectKey}
          </p>
          <h1 className="my-[var(--space-1)] text-[1.6rem]">{board.projectName}</h1>
          <p className="m-0 text-[var(--color-secondary)]">
            {board.selectedWorkflow.validForTaskCreation ? t("board.dragStart") : t("board.invalidWorkflow")}
          </p>
          {!board.selectedWorkflow.validForTaskCreation ? (
            <WorkflowValidationIssues workflow={board.selectedWorkflow} />
          ) : null}
        </div>
        <BoardMenu
          board={board}
          canCreateTask={connection.phase === "connected"}
          onNewTask={() => {
            setNewTaskOpen(true);
          }}
        />
      </header>
      <div
        className="flex min-h-0 gap-[var(--space-3)] overflow-x-auto overflow-y-hidden pb-[var(--space-2)] hide-scrollbar"
        role="list"
      >
        {sections.map((section) =>
          section.kind === "group" ? (
            <KanbanGroup
              board={board}
              actionsDisabled={actionsDisabled}
              canRunTasks={board.selectedWorkflow.validForTaskCreation}
              columns={section.columns}
              doneExpanded={doneExpanded}
              firstActiveColumnID={firstActive?.id ?? ""}
              group={section.group}
              hasMoreCards={hasMoreCards}
              isLoadingMoreCards={isLoadingMoreCards}
              key={section.id}
              onCardClick={(taskID) => {
                openTaskDetail(taskID, "", () => {
                  navigation.openProjectTask(board.projectID, board.selectedWorkflow.id, taskID);
                });
              }}
              onDropTask={dropTask}
              onInterruptTask={(taskID, runID) => void actions.interrupt.mutateAsync({ taskID, runID })}
              onLoadMoreCards={onLoadMoreCards}
              onResumeTask={(taskID, runID) => void actions.resume.mutateAsync({ taskID, runID })}
              onToggleDone={() => {
                setDoneExpanded((current) => !current);
              }}
            />
          ) : (
            <KanbanColumn
              cards={cardsForColumn(board, section.column, doneExpanded)}
              actionsDisabled={actionsDisabled}
              canRunTasks={board.selectedWorkflow.validForTaskCreation}
              column={section.column}
              doneExpanded={doneExpanded}
              hasMoreCards={hasMoreCards}
              isLoadingMoreCards={isLoadingMoreCards}
              isFirstActive={section.column.id === firstActive?.id}
              key={section.id}
              onCardClick={(taskID) => {
                openTaskDetail(taskID, "", () => {
                  navigation.openProjectTask(board.projectID, board.selectedWorkflow.id, taskID);
                });
              }}
              onDropTask={dropTask}
              onInterruptTask={(taskID, runID) => void actions.interrupt.mutateAsync({ taskID, runID })}
              onLoadMoreCards={onLoadMoreCards}
              onResumeTask={(taskID, runID) => void actions.resume.mutateAsync({ taskID, runID })}
              onToggleDone={() => {
                setDoneExpanded((current) => !current);
              }}
            />
          ),
        )}
      </div>
      <NewTaskDialog
        board={board}
        onClose={() => {
          setNewTaskOpen(false);
        }}
        open={newTaskOpen}
      />
      <TaskDetailDialog
        onClose={() => {
          navigation.closeProjectTask(board.projectID, board.selectedWorkflow.id);
        }}
        open={selectedTaskId.length > 0}
        resumeRunId={resumeRunId}
        taskId={selectedTaskId}
      />
    </div>
  );
}

function WorkflowValidationIssues({ workflow }: Readonly<{ workflow: WorkflowPickerItem }>) {
  const { t } = useTranslation();
  const messages =
    workflow.validationErrors.length > 0
      ? workflow.validationErrors.map((issue) => issue.message)
      : [t("board.invalidWorkflowUnknown")];
  return (
    <ul className="m-0 mt-[var(--space-2)] grid max-w-[56ch] gap-[var(--space-1)] pl-[var(--space-4)] text-sm text-[var(--color-secondary)]">
      {messages.map((message) => (
        <li key={message}>{message}</li>
      ))}
    </ul>
  );
}

function BoardMenu({
  board,
  canCreateTask,
  onNewTask,
}: Readonly<{ board: WorkflowBoard; canCreateTask: boolean; onNewTask: () => void }>) {
  const { t } = useTranslation();
  const navigation = useAppNavigation();
  const menuRef = useRef<HTMLDivElement | null>(null);
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(false);
  const visible = open || pinned;
  const closeIfUnpinned = (): void => {
    if (!pinned) {
      setOpen(false);
    }
  };
  function closeWhenFocusLeaves(event: FocusEvent<HTMLDivElement>): void {
    if (event.relatedTarget instanceof Node && menuRef.current?.contains(event.relatedTarget)) {
      return;
    }
    closeIfUnpinned();
  }

  return (
    <div
      className="app-region-no-drag relative"
      onBlur={closeWhenFocusLeaves}
      onFocus={() => {
        setOpen(true);
      }}
      onMouseEnter={() => {
        setOpen(true);
      }}
      onMouseLeave={closeIfUnpinned}
      ref={menuRef}
    >
      <button
        aria-expanded={visible}
        aria-haspopup="menu"
        className="rounded-[var(--radius-m)] border border-[var(--color-outline)] bg-[var(--color-island-1)] px-[14px] py-[10px] text-[var(--color-on-island)]"
        onClick={() => {
          setOpen((current) => !current);
        }}
        type="button"
      >
        {t("board.menu")}: {board.selectedWorkflow.name}
      </button>
      {visible ? (
        <div
          className="island-glass absolute right-0 top-[calc(100%+8px)] z-20 grid min-w-72 origin-top-right animate-[board-menu-reveal_var(--motion-fast)] gap-[var(--space-2)] rounded-[var(--radius-l)] p-[var(--space-2)]"
          role="menu"
        >
          <BoardMenuAction
            label={t("app.home")}
            onClick={() => {
              navigation.openHome();
            }}
          />
          <BoardMenuAction
            label={t("home.attentionPane")}
            onClick={() => {
              navigation.openHome();
            }}
          />
          <BoardMenuAction disabled={!canCreateTask} label={t("board.newTask")} onClick={onNewTask} />
          <BoardMenuAction
            label={pinned ? t("board.unpinMenu") : t("board.pinMenu")}
            onClick={() => {
              setPinned((current) => !current);
              setOpen(true);
            }}
          />
          <div className="h-px bg-[var(--color-outline)]" role="presentation" />
          <p className="m-0 px-[var(--space-2)] text-[0.72rem] font-extrabold uppercase tracking-[0.16em] text-[var(--color-secondary)]">
            {t("board.workflowPicker")}
          </p>
          {board.workflows.map((workflow) => (
            <WorkflowOption board={board} key={workflow.id} workflow={workflow} />
          ))}
        </div>
      ) : null}
    </div>
  );

  function WorkflowOption({ workflow }: Readonly<{ board: WorkflowBoard; workflow: WorkflowPickerItem }>) {
    return (
      <button
        className="grid gap-[var(--space-1)] rounded-[var(--radius-m)] border border-transparent bg-transparent p-[var(--space-2)] text-left text-[var(--color-on-island)]"
        onClick={() => {
          navigation.openProject(board.projectID, workflow.id);
        }}
        type="button"
      >
        <strong>{workflow.name}</strong>
        {workflow.isProjectDefault ? <Badge tone="info">{t("board.defaultWorkflow")}</Badge> : null}
      </button>
    );
  }
}

function BoardMenuAction({
  disabled = false,
  label,
  onClick,
}: Readonly<{ disabled?: boolean; label: string; onClick: () => void }>) {
  return (
    <button
      className="rounded-[var(--radius-m)] border border-transparent bg-transparent p-[var(--space-2)] text-left text-[var(--color-on-island)] disabled:cursor-not-allowed disabled:opacity-55"
      disabled={disabled}
      onClick={onClick}
      role="menuitem"
      type="button"
    >
      {label}
    </button>
  );
}
