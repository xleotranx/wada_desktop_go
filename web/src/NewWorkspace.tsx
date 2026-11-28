import { useState } from "react";
import type { ProjectInfo, WorkspaceCreate, WorkspaceInfo } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";

type Start = "branch-off" | "checkout-branch" | "checkout-pr";

/**
 * NewWorkspace asks how a project's new Git worktree starts: a new branch
 * from a ref, an existing branch, or a pull request. A create that fails is
 * reported, not retried: it may have made the worktree.
 */
export function NewWorkspace({
  cli,
  project,
  onCreated,
  onClose,
}: {
  cli: WadaClient;
  project: ProjectInfo;
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

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    const req: WorkspaceCreate = { project: project.id, isolation: "worktree", mode: start };
    if (start === "checkout-pr") {
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
    <div className="modal-backdrop" onClick={onClose}>
      <form className="modal" onClick={(e) => e.stopPropagation()} onSubmit={(e) => void submit(e)}>
        <h3>New workspace in {project.name}</h3>
        <p className="muted">A Git worktree of its own, so agents work beside each other.</p>
        <label>
          Start from
          <select value={start} onChange={(e) => setStart(e.target.value as Start)}>
            <option value="branch-off">A new branch</option>
            <option value="checkout-branch">An existing branch</option>
            <option value="checkout-pr">A pull request</option>
          </select>
        </label>
        {start === "checkout-pr" ? (
          <label>
            Pull request
            <input inputMode="numeric" value={pr} onChange={(e) => setPR(e.target.value)} placeholder="123" required />
          </label>
        ) : (
          <label>
            {start === "branch-off" ? "Base" : "Branch"}
            <input
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              placeholder={start === "branch-off" ? "Default: origin's default branch" : "feature/login"}
              required={start === "checkout-branch"}
            />
          </label>
        )}
        {start === "branch-off" && (
          <label>
            Name
            <input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="Optional: named from the first message" />
          </label>
        )}
        <label>
          Title
          <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Optional" />
        </label>
        {error && <div className="banner-error">{error}</div>}
        <div className="row end">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn btn-primary" disabled={busy}>
            {busy ? "Creating…" : "Create"}
          </button>
        </div>
      </form>
    </div>
  );
}
