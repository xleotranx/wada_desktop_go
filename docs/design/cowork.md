# Cowork behavior and tools

Wada is a desktop assistant for everyday work. Its default role is to understand
relevant documents, answer questions, organize information, create deliverables,
and review their accuracy and completeness. Code work requires an explicit user
request; a folder containing a repository does not select a coding role.

`internal/plugins/cowork` contributes an `agent.ProjectTool` of kind `cowork`.
The contribution contains the assistant instructions and stateless worker
operations. PiG captures those instructions on startup and supplies them as the
SDK system prompt, with automatic coding resources and built-in tools disabled.
SDK types stay inside PiG. The document reader is resolved on the UI thread and
called on the tool worker through `documents.Reader`, without importing its plugin.

The assistant should answer directly when it already has enough information.
File tools are used only for relevant sources and requested deliverables. Source
content is evidence, never authority to expand the task. The instructions separate
facts from assumptions, request source attribution, require numerical checks and
forbid claims of unavailable exports, connectors, sending or scheduling.

## Default toolset

| Tool | Operation |
| --- | --- |
| `list_files` | List one relative folder, with pagination, up to 200 entries per call. |
| `read_document` | Read UTF-8 text or convert a supported Office/PDF document through Wada's reader. Character offsets paginate the result, up to 24,000 characters per response. |
| `search_documents` | Literal, case-insensitive search under a relative folder, up to 200 readable documents and 40 matching excerpts with paths and line numbers. |
| `write_document` | Atomically save a text deliverable up to 8 MiB, preserving an existing file's permissions. |
| `edit_document` | Replace one unique exact passage in a text deliverable; a stale or ambiguous match fails without writing. |
| `calculate` | Evaluate bounded numeric arithmetic with parentheses and `+ - * / %`. No identifiers, functions or scripts execute. Results use IEEE-754 floating point, disclosed with the result. |
| `read_url` | GET a known HTTP(S) text page, strip scripts/styles from HTML, and return its final URL and paginated text. No authentication, form submission or web search. |

Text output formats are Markdown, text, CSV/TSV, HTML, JSON, SVG, CSS and JavaScript.
Office/PDF input is supported; native Office/PDF output requires an exporter.
The tools refuse to save text under a binary document extension. External web
pages have a 2 MiB body limit, a 20-second timeout and at most five redirects.
Public-page connections validate the resolved IP at dial time, including after
redirects. Loopback, private, link-local and shared-address destinations are refused;
ambient HTTP proxy credentials are not used. This tool does not access intranet
or localhost sources.
Reading text or a document is bounded to 8 MiB on disk. Errors and unsupported
formats are reported rather than triggering installation or shell fallbacks.

Paths are relative to the conversation's selected work folder. Absolute paths,
traversal outside the folder, symlink escapes, hidden paths, dependency folders and
Wada's private app state are unavailable. Search skips symlinks and protected
subtrees. Wada's dedicated `<DataDir>/workspaces` subtree is available for saved
work; new folderless conversations use its Inbox instead of the user's home.

PiG exposes these tools for ordinary turns and blocks legacy shell/coding tools
in its pre-tool hook, including in independent mode. A specialized `ProjectScope`,
such as video authoring, replaces the ordinary toolset with that project's handler.
Read-only child policies still block document writes and edits. Permissions are
shown as **Ask before changes** and **Work independently**; both retain folder
boundaries. Internal mode IDs remain `ask` and `full` in saved preferences.

The built-in reviewed workflow is **Plan, work, review**. Its work phase creates
the requested deliverable; the reviewer checks evidence, facts, calculations,
requirements and readability. Jobs keep converted source copies under
`job-sources/<job-id>/sources`, accessible through the same work-folder tools,
while their app metadata stays private.

## Verification

Cowork tests cover document creation/revision, Unicode pagination, literal source
search, Office-reader routing, cancellation, output formats, folder and app-data
boundaries, symlink escapes, arithmetic rejection and web extraction without a
network server. PiG integration tests exercise the actual SDK toolset, system
prompt, blocked coding tools, permissions, file attribution, default Inbox,
explicit skill selection and independent reviewed tasks.
