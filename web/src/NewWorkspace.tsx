import { useEffect, useState } from "react";
import type { ProjectInfo, WorkspaceCreate, WorkspaceInfo, WorkspaceRefs } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";

import { WorkspaceDialog } from "./WorkspaceDialogs";

type Start = NonNullable<WorkspaceCreate["mode"]> | "existing";

/**
 * NewWorkspace asks how a project's new Git worktree starts: a new branch
 * from a ref, an existing branch, or a pull request. A create that fails is
 * reported, not retried: it may have made the worktree.
 */
export function NewWorkspace({
  cli,
  project,
  workspaces,
  onCreated,
  onClose,
}: {
  cli: WadaClient;
  project: ProjectInfo;
  workspaces: WorkspaceInfo[];
  onCreated: (w: WorkspaceInfo) => void;
  onClose: () => void;
}) {
  const [start, setStart] = useState<Start>("branch-off");
  const [from, setFrom] = useState("");
  const [pr, setPR] = useState("");
  const [slug, setSlug] = useState("");
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const [path, setPath] = useState("");
  const [refs, setRefs] = useState<WorkspaceRefs | null>(null);
  const [refsError, setRefsError] = useState("");
  useEffect(() => {
    if (start !== "existing" || refs) return;
    let current = true;
    void cli.workspaceRefs(project.id).then((data) => {
      if (current) setRefs(data || {});
    }).catch((err: unknown) => {
      if (current) setRefsError(err instanceof Error ? err.message : String(err));
    });
    return () => { current = false; };
  }, [cli, project.id, start, refs]);

  const taken = new Set(workspaces
    .filter((w) => !w.archived && w.project === project.id)
    .map((w) => w.worktreeRoot || w.directory));
  const existing = (refs?.worktrees || []).filter((w) => !w.isMain && !taken.has(w.path));
  const selected = existing.some((w) => w.path === path);
  const worktreeHint = refsError ? "Could not read worktrees"
    : !refs ? "Reading worktrees…"
    : existing.length ? "Choose a worktree" : "No available worktrees";

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    if (busy) return;
    const req: WorkspaceCreate = start === "existing"
      ? { project: project.id, isolation: "existing", path }
      : { project: project.id, isolation: "worktree", mode: start };
    if (start === "existing") {
      if (!selected) return;
    } else if (start === "checkout-pr") {
      const n = Number(pr);
      if (!Number.isInteger(n) || n <= 0) {
        setError("Enter the pull request's number.");
        return;
      }
      req.pr = n;
    } else if (from.trim()) {
      req.from = from.trim();
    } else if (start === "checkout-branch") {
      setError("Enter the branch to check out.");
      return;
    }
    if (slug.trim()) req.slug = slug.trim();
    if (title.trim()) req.title = title.trim();
    setBusy(true);
    setError("");
    try {
      onCreated(await cli.createWorkspace(req));
    } catch (err) {
      setError(`The workspace may not have been created: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <WorkspaceDialog title={`New workspace in ${project.name}`} busy={busy} onClose={onClose}>
      <form className="workspace-dialog-body" onSubmit={(e) => void submit(e)}>
        <label>
          Start from
          <select value={start} onChange={(e) => {
            setStart(e.target.value as Start);
            setRefsError("");
            setError("");
          }} disabled={busy}>
            <option value="branch-off">A new branch</option>
            <option value="checkout-branch">An existing branch</option>
            <option value="checkout-pr">A pull request</option>
            <option value="existing">An existing worktree</option>
          </select>
        </label>
        {start === "existing" ? (
          <label>
            Worktree
            <select value={path} onChange={(e) => setPath(e.target.value)} required disabled={busy || !existing.length}>
              <option value="">{worktreeHint}</option>
              {existing.map((w) => (
                <option key={w.path} value={w.path}>{w.branch ? `${w.branch} · ${w.path}` : w.path}</option>
              ))}
            </select>
          </label>
        ) : start === "checkout-pr" ? (
          <label>
            Pull request
            <input inputMode="numeric" value={pr} disabled={busy} onChange={(e) => setPR(e.target.value)} placeholder="123" required />
          </label>
        ) : (
          <label>
            {start === "branch-off" ? "Base" : "Branch"}
            <input
              value={from}
              disabled={busy}
              onChange={(e) => setFrom(e.target.value)}
              placeholder={start === "branch-off" ? "Default: origin's default branch" : "feature/login"}
              required={start === "checkout-branch"}
            />
          </label>
        )}
        {start === "branch-off" && (
          <label>
            Name
            <input value={slug} disabled={busy} onChange={(e) => setSlug(e.target.value)} placeholder="Optional: named from the first message" />
          </label>
        )}
        <label>
          Title
          <input value={title} disabled={busy} onChange={(e) => setTitle(e.target.value)} placeholder="Optional" />
        </label>
        {(error || (start === "existing" && refsError)) && <div className="banner-error" role="alert">{error || refsError}</div>}
        <div className="row end">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy || (start === "existing" && !selected)}>
            {busy ? "Working…" : start === "existing" ? "Open worktree" : "Create"}
          </button>
        </div>
      </form>
    </WorkspaceDialog>
  );
}
