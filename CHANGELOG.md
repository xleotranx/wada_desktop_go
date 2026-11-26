# Changelog

## Unreleased

- Native desktop chat with an embedded PiG coding backend, model and access-mode
  selection, attachments, file mentions and skills.
- Folder-scoped conversation tabs, file diffs, usage information, notifications,
  read-only side chats and conversational workflows with independent reviews.
- A new chat asks for a work folder first; its starters follow the folder's
  documents, project or empty state. Reviewed document starters select the
  plan, work, review flow.
- Workflow phases complete with models that answer in text: a clearer
  `submit_result` instruction, and a validated fenced JSON result as fallback.
- A first run with no connected provider opens **Connect a model provider** using
  the same provider search, list and login as Settings. The setup gives way to the
  folder gate when a provider connects, and a single model is chosen automatically.
- A task ends with a deliverable card: the files changed since the task started,
  each with **Open**, and a **Sources** row for the work folder and references with
  **Reveal**. Details expand the request, plan, work and review findings.
- The work panel starts hidden so a stored conversation opens on the transcript;
  ⌘B or Conversation settings shows it.
- Screenshot annotation and a local HTML design canvas.
- Independently versioned MIT-licensed wada-kit core and shell.
- Source publication documentation, third-party license inventory and CI checks.
- Update `golang.org/x/net` to v0.62.0 for GO-2026-6860 and GO-2026-6866.

The packaging version is `0.1.0`. There is no published Wada binary release yet.
