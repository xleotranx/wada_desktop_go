import type { ProjectInfo, SessionInfo, WorkspaceInfo } from "./protocol";

/** A workspace and the sessions in it, newest first. */
export interface WorkspaceGroup {
  workspace?: WorkspaceInfo; // absent for sessions in no workspace
  name: string;
  detail: string; // the branch, or the directory
  sessions: SessionInfo[];
}

/** A project's workspaces that have sessions, its own first. */
export interface ProjectGroup {
  project?: ProjectInfo; // absent for the "Other" group
  name: string;
  workspaces: WorkspaceGroup[];
}

/** What a workspace is called: its title, else a worktree's branch, else its project's name. */
export function workspaceName(w: WorkspaceInfo, project?: ProjectInfo): string {
  if (w.title) return w.title;
  if (w.isolation !== "project" && w.branch) return w.branch;
  return project?.name || baseName(w.directory);
}

/**
 * groupSessions files sessions under their project and workspace, matching
 * a session to a workspace by its workspace ID, else by directory. Projects
 * keep the daemon's order with the inbox last; sessions that match no active
 * workspace go in a trailing "Other" group.
 */
export function groupSessions(
  sessions: SessionInfo[],
  projects: ProjectInfo[],
  workspaces: WorkspaceInfo[]
): ProjectGroup[] {
  const active = workspaces.filter((w) => !w.archived);
  const byID = new Map(active.map((w) => [w.id, w]));
  const byDir = new Map(active.map((w) => [w.directory, w]));
  const projectByID = new Map(projects.filter((p) => !p.archived).map((p) => [p.id, p]));
  const inWorkspace = new Map<string, SessionInfo[]>();
  const other: SessionInfo[] = [];
  const newest = [...sessions].sort((a, b) => (b.updated || 0) - (a.updated || 0));
  for (const s of newest) {
    const w = (s.workspace && byID.get(s.workspace)) || byDir.get(s.directory);
    if (!w || !projectByID.has(w.project)) {
      other.push(s);
      continue;
    }
    const list = inWorkspace.get(w.id) || [];
    list.push(s);
    inWorkspace.set(w.id, list);
  }

  const ordered = [...projectByID.values()].sort((a, b) => Number(!!a.inbox) - Number(!!b.inbox));
  const groups: ProjectGroup[] = [];
  for (const p of ordered) {
    const spaces = active
      .filter((w) => w.project === p.id && inWorkspace.has(w.id))
      .sort((a, b) => Number(a.isolation !== "project") - Number(b.isolation !== "project") || a.created - b.created)
      .map((w) => ({
        workspace: w,
        name: workspaceName(w, p),
        detail: w.isolation !== "project" && w.branch ? w.branch : w.directory,
        sessions: inWorkspace.get(w.id) || [],
      }));
    if (spaces.length) groups.push({ project: p, name: p.inbox ? "Inbox" : p.name, workspaces: spaces });
  }
  if (other.length) {
    groups.push({ name: "Other", workspaces: [{ name: "Other", detail: "", sessions: other }] });
  }
  return groups;
}

function baseName(dir: string): string {
  const parts = dir.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || dir;
}
