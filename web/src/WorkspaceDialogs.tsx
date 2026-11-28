import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import type { ProjectInfo, WorkspaceInfo } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";
import { workspaceName } from "./client/workspaces";

export function WorkspaceDialog({
  title, busy, onClose, children,
}: {
  title: string;
  busy: boolean;
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const label = useId();
  useEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    return () => dialog.close();
  }, []);

  return (
    <dialog
      ref={ref}
      className="modal workspace-dialog"
      aria-labelledby={label}
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onClose();
      }}
    >
      <h3 id={label}>{title}</h3>
      {children}
    </dialog>
  );
}

export function AddProject({
  cli, host, onClose, onAdded,
}: {
  cli: WadaClient;
  host: string;
  onClose: () => void;
  onAdded: (project: ProjectInfo, workspace: WorkspaceInfo) => void;
}) {
  const [path, setPath] = useState("");
  const [suggestions, setSuggestions] = useState<{ prefix: string; folders: string[] } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const picked = useRef("");

  useEffect(() => {
    if (!path || path === picked.current || busy) return;
    let current = true;
    const timer = window.setTimeout(() => {
      void cli.projectFolders(path).then((folders) => {
        if (current) setSuggestions({ prefix: path, folders });
      }).catch(() => {});
    }, 150);
    return () => {
      current = false;
      window.clearTimeout(timer);
    };
  }, [cli, path, busy]);

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (busy || !path.trim()) return;
    setBusy(true);
    setError("");
    setSuggestions(null);
    try {
      const result = await cli.addProject(path);
      onAdded(result.project, result.workspace);
    } catch (err) {
      setError(actionError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <WorkspaceDialog title="Add project" busy={busy} onClose={onClose}>
      <form className="workspace-dialog-body" onSubmit={(e) => void submit(e)}>
        <label>
          Folder path on {host}
          <input
            autoFocus
            autoComplete="off"
            value={path}
            disabled={busy}
            onChange={(e) => {
              picked.current = "";
              setPath(e.target.value);
              setError("");
            }}
            required
          />
        </label>
        {!busy && suggestions?.prefix === path && suggestions.folders.length > 0 && (
          <ul className="folder-suggestions" aria-label="Suggested folders">
            {suggestions.folders.map((folder) => (
              <li key={folder}>
                <button type="button" className="btn" onClick={() => {
                  picked.current = folder;
                  setPath(folder);
                  setSuggestions(null);
                }}>{folder}</button>
              </li>
            ))}
          </ul>
        )}
        {error && <div className="banner-error" role="alert">{error}</div>}
        <div className="row end">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="submit" className="btn btn-primary" disabled={busy || !path.trim()}>
            {busy ? "Adding…" : "Add project"}
          </button>
        </div>
      </form>
    </WorkspaceDialog>
  );
}

export function RestoreWorkspace({
  cli, projects, workspaces, onClose, onRestored,
}: {
  cli: WadaClient;
  projects: ProjectInfo[];
  workspaces: WorkspaceInfo[];
  onClose: () => void;
  onRestored: (workspace: WorkspaceInfo) => void;
}) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const activeProjects = new Map(projects.filter((p) => !p.archived).map((p) => [p.id, p]));
  const archived = workspaces.filter((w) => w.archived && w.isolation !== "project" && activeProjects.has(w.project))
    .sort((a, b) => (b.archived || 0) - (a.archived || 0));

  async function restore(id: string): Promise<void> {
    if (busy) return;
    setBusy(id);
    setError("");
    try {
      onRestored(await cli.restoreWorkspace(id));
    } catch (err) {
      setError(actionError(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <WorkspaceDialog title="Restore workspace" busy={!!busy} onClose={onClose}>
      {archived.length ? (
        <ul className="restore-workspaces">
          {archived.map((w) => {
            const project = activeProjects.get(w.project);
            const name = workspaceName(w, project);
            return (
              <li key={w.id}>
                <div><strong>{name}</strong><small className="muted">{project?.name} · {w.directory}</small></div>
                <button
                  className="btn"
                  aria-label={`Restore ${name}`}
                  disabled={!!busy}
                  onClick={() => void restore(w.id)}
                >{busy === w.id ? "Restoring…" : "Restore"}</button>
              </li>
            );
          })}
        </ul>
      ) : <p className="muted">No archived workspaces in active projects.</p>}
      {error && <div className="banner-error" role="alert">{error}</div>}
      <div className="row end"><button className="btn" onClick={onClose} disabled={!!busy}>Close</button></div>
    </WorkspaceDialog>
  );
}

function actionError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
