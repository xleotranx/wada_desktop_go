export interface SavedHost {
  id: string;
  name: string;
  mode: "relay" | "direct";
  offer?: string;
  relayUrl?: string;
  directUrl?: string;
  serverId?: string;
  token?: string;
  requiresPassword?: boolean;
  addedAt: number;
}

const HOSTS_KEY = "wada.hosts.v1";
const ACTIVE_KEY = "wada.activeHost.v1";

export function loadSavedHosts(): SavedHost[] {
  try {
    const raw = localStorage.getItem(HOSTS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function saveHosts(list: SavedHost[]): void {
  localStorage.setItem(HOSTS_KEY, JSON.stringify(list));
}

export function upsertHost(host: SavedHost): SavedHost[] {
  const list = loadSavedHosts().filter((h) => h.id !== host.id && h.name !== host.name);
  const next = [host, ...list];
  saveHosts(next);
  return next;
}

export function deleteHost(id: string): SavedHost[] {
  const next = loadSavedHosts().filter((h) => h.id !== id);
  saveHosts(next);
  return next;
}

export function loadActiveHostId(): string {
  return localStorage.getItem(ACTIVE_KEY) || "";
}

export function saveActiveHostId(id: string): void {
  if (!id) localStorage.removeItem(ACTIVE_KEY);
  else localStorage.setItem(ACTIVE_KEY, id);
}
