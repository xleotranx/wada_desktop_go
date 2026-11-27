import { beforeEach, describe, expect, it, vi } from "vitest";
import { type SavedHost, deleteHost, loadSavedHosts, upsertHost } from "./storage";

function host(id: string, name: string): SavedHost {
  return { id, name, mode: "relay", addedAt: 0 };
}

describe("saved hosts", () => {
  beforeEach(() => {
    const data = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => data.get(k) ?? null,
      setItem: (k: string, v: string) => void data.set(k, v),
      removeItem: (k: string) => void data.delete(k),
    });
  });

  it("keeps two daemons that share a display name", () => {
    upsertHost(host("a", "My Workstation"));
    upsertHost(host("b", "My Workstation"));
    expect(loadSavedHosts().map((h) => h.id)).toEqual(["b", "a"]);
  });

  it("replaces a host re-paired under the same id", () => {
    upsertHost(host("a", "Old"));
    upsertHost(host("a", "New"));
    expect(loadSavedHosts()).toEqual([host("a", "New")]);
    expect(deleteHost("a")).toEqual([]);
  });
});
