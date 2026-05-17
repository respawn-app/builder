import { useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";

import type { AttentionItem, ProjectSummary } from "../../api";
import { errorMessage } from "../../api/errors";
import { basename, formatRelativeTime, projectKeyFromName } from "../../app/formatters";
import { useAppNavigation } from "../../app/navigation";
import { useAppServices } from "../../app/useAppServices";
import { useStatusController } from "../../app/useStatusController";
import { useConnectionSnapshot } from "../../app/useConnectionSnapshot";
import { Badge, Button, ErrorState, TextInput } from "../../ui";
import { useGlobalAttentionPages, useProjectCreation, useProjectPages } from "./useHomeData";

type ProjectDraft = Readonly<{
  name: string;
  key: string;
  workspaceRoot: string;
}>;

export function HomeRoute() {
  const { t } = useTranslation();
  const { api, nativeBridge } = useAppServices();
  const { push } = useStatusController();
  const connection = useConnectionSnapshot();
  const navigation = useAppNavigation();
  const creation = useProjectCreation();
  const projects = useProjectPages();
  const attention = useGlobalAttentionPages();
  const projectItems = projects.data?.pages.flatMap((page) => page.projects) ?? [];
  const attentionItems = attention.data?.pages.flatMap((page) => page.items) ?? [];
  const [draft, setDraft] = useState<ProjectDraft | null>(null);
  const disabled = connection.phase !== "connected";

  async function chooseWorkspace(): Promise<void> {
    try {
      const selected = await nativeBridge.directories.selectDirectory({ title: t("home.chooseWorkspace") });
      if (selected === null) {
        return;
      }
      const plan = await api.planWorkspace(selected.path);
      if (plan.binding !== null) {
        navigation.openProject(plan.binding.projectID);
        return;
      }
      const name = basename(plan.canonicalRoot);
      setDraft({ name, key: projectKeyFromName(name), workspaceRoot: plan.canonicalRoot });
    } catch (error) {
      push({ id: "project-create-error", tone: "danger", title: t("form.serverError"), body: errorMessage(error) });
    }
  }

  async function submitDraft(event: SyntheticEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    if (draft === null) {
      return;
    }
    const binding = await creation.mutateAsync(draft);
    setDraft(null);
    navigation.openProject(binding.projectID);
  }

  return (
    <div className="home-page">
      <HomeHeader
        creationError={creation.error}
        draft={draft}
        isCreating={creation.isPending}
        onSubmitDraft={(event) => void submitDraft(event)}
        setDraft={setDraft}
      />
      <div className="home-grid">
        <section aria-labelledby="projects-title" className="home-pane">
          <div className="home-pane__header">
            <h2 id="projects-title">{t("home.projectsPane")}</h2>
            <button
              aria-label={t("home.newProject")}
              className="icon-button"
              disabled={disabled}
              onClick={() => void chooseWorkspace()}
              type="button"
            >
              <Plus aria-hidden="true" size={20} strokeWidth={1.5} />
            </button>
          </div>
          <ProjectList items={projectItems} query={projects} />
        </section>
        <section aria-labelledby="attention-title" className="home-pane">
          <h2 id="attention-title">{t("home.attentionPane")}</h2>
          <AttentionList items={attentionItems} query={attention} />
        </section>
      </div>
    </div>
  );
}

type HomeHeaderProps = Readonly<{
  creationError: Error | null;
  draft: ProjectDraft | null;
  isCreating: boolean;
  onSubmitDraft: (event: SyntheticEvent<HTMLFormElement>) => void;
  setDraft: (draft: ProjectDraft) => void;
}>;

function HomeHeader({
  creationError,
  draft,
  isCreating,
  onSubmitDraft,
  setDraft,
}: HomeHeaderProps) {
  const { t } = useTranslation();

  return (
    <header className="page-header">
      {draft !== null ? (
        <form className="inline-form" onSubmit={onSubmitDraft}>
          <TextInput label={t("home.projectName")} onChange={(event) => { setDraft({ ...draft, name: event.target.value }); }} value={draft.name} />
          <TextInput label={t("home.projectKey")} onChange={(event) => { setDraft({ ...draft, key: event.target.value }); }} value={draft.key} />
          <TextInput disabled label={t("home.workspaceRoot")} value={draft.workspaceRoot} />
          {creationError !== null ? <p className="form-error">{errorMessage(creationError)}</p> : null}
          <Button disabled={isCreating} type="submit" variant="primary">
            {t("home.createProject")}
          </Button>
        </form>
      ) : null}
    </header>
  );
}

type ProjectListProps = Readonly<{
  items: readonly ProjectSummary[];
  query: ReturnType<typeof useProjectPages>;
}>;

function ProjectList({ items, query }: ProjectListProps) {
  const { t } = useTranslation();
  if (query.isPending) {
    return <p>{t("states.loading")}</p>;
  }
  if (query.isError) {
    return <ErrorState body={errorMessage(query.error)} title={t("states.error")} />;
  }
  if (items.length === 0) {
    return <HomeInlineEmptyState body={t("home.emptyBody")} title={t("home.emptyTitle")} />;
  }
  return (
    <div className="row-list">
      {items.map((project) => (
        <ProjectRow key={project.id} project={project} />
      ))}
      {query.hasNextPage ? (
        <Button disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()} variant="ghost">
          {query.isFetchingNextPage ? t("app.loadingMore") : t("app.loadMore")}
        </Button>
      ) : null}
    </div>
  );
}

function ProjectRow({ project }: Readonly<{ project: ProjectSummary }>) {
  const navigation = useAppNavigation();

  return (
    <article className="project-row">
      <button className="project-row__main" onClick={() => { navigation.openProject(project.id, project.defaultWorkflowID); }} type="button">
        <span className="mono">{project.key}</span>
        <strong>{project.name}</strong>
        <span>{project.primaryWorkspace.rootPath}</span>
      </button>
    </article>
  );
}

type AttentionListProps = Readonly<{
  items: readonly AttentionItem[];
  query: ReturnType<typeof useGlobalAttentionPages>;
}>;

function AttentionList({ items, query }: AttentionListProps) {
  const { t } = useTranslation();
  const navigation = useAppNavigation();
  if (query.isPending) {
    return <p>{t("states.loading")}</p>;
  }
  if (query.isError) {
    return <ErrorState body={errorMessage(query.error)} title={t("states.error")} />;
  }
  if (items.length === 0) {
    return <HomeInlineEmptyState body={t("home.noAttentionBody")} title={t("home.noAttentionTitle")} />;
  }
  return (
    <div className="row-list">
      {items.map((item) => (
        <button className="attention-row" key={item.id} onClick={() => { navigation.openTask(item.taskID); }} type="button">
          <Badge tone="warning">{item.kind}</Badge>
          <strong>{item.taskTitle}</strong>
          <span className="mono">{item.taskShortID}</span>
          <span>{item.message}</span>
          <span>{formatRelativeTime(item.occurredAt)}</span>
        </button>
      ))}
      {query.hasNextPage ? (
        <Button disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()} variant="ghost">
          {query.isFetchingNextPage ? t("app.loadingMore") : t("app.loadMore")}
        </Button>
      ) : null}
    </div>
  );
}

function HomeInlineEmptyState({ title, body }: Readonly<{ title: string; body: string }>) {
  return (
    <div className="home-empty-inline">
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  );
}
