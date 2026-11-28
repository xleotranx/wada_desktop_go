import { useEffect, useState, type FormEvent } from "react";
import type { WorkspaceFile, WorkspaceInfo } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";

interface FileDraft {
  path: string;
  file: WorkspaceFile;
  text: string;
}

export interface FileBuffer {
  draft?: FileDraft;
  busy?: boolean;
  error?: string;
}

export function FilesPanel({ cli, workspace, buffer, onBuffer, showChat, onToggleChat }: {
  cli: WadaClient;
  workspace: WorkspaceInfo;
  buffer: FileBuffer;
  onBuffer: (buffer: FileBuffer) => void;
  showChat: boolean;
  onToggleChat: () => void;
}) {
  const [query, setQuery] = useState("");
  const [search, setSearch] = useState("");
  const [paths, setPaths] = useState<string[]>([]);
  const [searchError, setSearchError] = useState("");
  const { draft, busy, error } = buffer;
  const dirty = draft !== undefined && draft.text !== draft.file.text;
  const id = workspace.id;

  useEffect(() => {
    let current = true;
    void cli.findWorkspaceFiles(id, search).then((results) => {
      if (current) {
        setSearchError("");
        setPaths(results);
      }
    }).catch((err: unknown) => {
      if (current) setSearchError(message(err));
    });
    return () => { current = false; };
  }, [cli, id, search]);

  function update(next: FileDraft) {
    onBuffer({ draft: next });
  }

  async function open(path: string) {
    if (!path || dirty || busy) return;
    onBuffer({ draft, busy: true });
    try {
      const file = await cli.openWorkspaceFile(id, path);
      update({ path, file, text: file.text });
    } catch (err) {
      onBuffer({ draft, error: message(err) });
    }
  }

  async function save() {
    if (!draft || !dirty || busy) return;
    onBuffer({ draft, busy: true });
    try {
      const file = await cli.saveWorkspaceFile(id, draft.path, draft.text, draft.file.revision);
      update({ path: draft.path, file, text: file.text });
    } catch (err) {
      onBuffer({ draft, error: `Save failed: ${message(err)}` });
    }
  }

  function submitSearch(e: FormEvent) {
    e.preventDefault();
    setSearch(query.trim());
  }

  return (
    <div className="panel workspace-files">
      <section className="workspace-file-list" aria-label="Workspace files">
        <form onSubmit={submitSearch}>
          <label htmlFor="workspace-file-query">Find files</label>
          <input id="workspace-file-query" value={query} onChange={(e) => setQuery(e.target.value)} />
          <div className="row">
            <button className="btn" type="submit">Search</button>
            <button className="btn" type="button" disabled={!query.trim() || dirty || busy}
              onClick={() => void open(query.trim())}>Open path</button>
          </div>
        </form>
        {searchError && <p className="banner-error" role="alert">{searchError}</p>}
        <ul>
          {paths.map((path) => (
            <li key={path}>
              <button className="btn" disabled={dirty || busy} aria-pressed={path === draft?.path}
                onClick={() => void open(path)}>{path}</button>
            </li>
          ))}
        </ul>
      </section>
      <section className="workspace-file-editor">
        <div className="row end">
          <button className="btn btn-small" aria-pressed={showChat} onClick={onToggleChat}>Chat beside files</button>
        </div>
        {error && <p className="banner-error" role="alert">{error}</p>}
        {draft ? (
          <>
            <h3>{draft.path}</h3>
            <div className="row">
              <button className="btn btn-primary" disabled={!dirty || busy} onClick={() => void save()}>Save</button>
              {dirty && <button className="btn" disabled={busy} onClick={() => {
                update({ ...draft, text: draft.file.text });
              }}>Discard draft</button>}
              <button className="btn" disabled={dirty || busy} onClick={() => void open(draft.path)}>Reload</button>
            </div>
            <textarea aria-label="File contents" readOnly={busy} spellCheck={false} value={draft.text}
              onChange={(e) => update({ ...draft, text: e.target.value })}
              onKeyDown={(e) => {
                if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
                  e.preventDefault();
                  void save();
                }
              }}
            />
          </>
        ) : <p className="muted">Choose a text file to edit.</p>}
      </section>
    </div>
  );
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
