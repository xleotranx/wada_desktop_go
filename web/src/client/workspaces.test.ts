import { describe, expect, it } from "vitest";
import type { ProjectInfo, SessionInfo, WorkspaceInfo } from "./protocol";
import { groupSessions, workspaceName } from "./workspaces";

const app: ProjectInfo = { id: "prj:/w/app", root: "/w/app", name: "app", created: 1 };
const inbox: ProjectInfo = { id: "prj:inbox", root: "/d/inbox", name: "inbox", inbox: true, created: 0 };
const main: WorkspaceInfo = { id: "wks:/w/app", project: app.id, isolation: "project", directory: "/w/app", created: 1 };
const fix: WorkspaceInfo = {
  id: "wks:fix", project: app.id, isolation: "worktree", directory: "/d/wt/fix", branch: "wada/fix", created: 2,
};
const box: WorkspaceInfo = { id: "wks:inbox", project: inbox.id, isolation: "project", directory: "/d/inbox", created: 0 };

function session(id: string, directory: string, updated: number, workspace?: string): SessionInfo {
  return { id, title: id, directory, updated, workspace };
}

describe("groupSessions", () => {
  it("files sessions under projects and workspaces, the inbox last", () => {
    const groups = groupSessions(
      [
        session("a", "/w/app", 1),
        session("b", "/d/wt/fix", 3, "wks:fix"),
        session("c", "/w/app", 2),
        session("d", "/d/inbox", 4),
        session("e", "/elsewhere", 5),
      ],
      [inbox, app],
      [box, fix, main]
    );
    expect(groups.map((g) => g.name)).toEqual(["app", "Inbox", "Other"]);
    expect(groups[0].workspaces.map((w) => [w.name, w.detail, w.sessions.map((s) => s.id)])).toEqual([
      ["app", "/w/app", ["c", "a"]],
      ["wada/fix", "wada/fix", ["b"]],
    ]);
    expect(groups[2].workspaces[0].sessions.map((s) => s.id)).toEqual(["e"]);
  });

  it("leaves out archived workspaces and empty ones", () => {
    const groups = groupSessions([session("a", "/d/wt/fix", 1)], [app], [main, { ...fix, archived: 9 }]);
    expect(groups.map((g) => g.name)).toEqual(["Other"]);
  });
});

describe("workspaceName", () => {
  it("prefers the title, then a worktree's branch, then the project's name", () => {
    expect(workspaceName({ ...fix, title: "Parser" }, app)).toBe("Parser");
    expect(workspaceName(fix, app)).toBe("wada/fix");
    expect(workspaceName(main, app)).toBe("app");
  });
});
