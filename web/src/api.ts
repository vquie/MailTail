import type { AppInfo, AppSettings, Message, MessagePage, SessionInfo, Stats, User } from "./types";

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, withCSRF(init));
  if (!response.ok) {
    if (response.status === 401) {
      window.location.href = "/login";
      throw new Error("Authentication required");
    }
    const payload = await response.json().catch(() => ({}));
    throw new Error(payload.error ?? `Request failed with status ${response.status}`);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return response.json() as Promise<T>;
}

export async function fetchMessages(query: string, cursor = "", limit = 25): Promise<MessagePage> {
  const params = new URLSearchParams();
  params.set("limit", String(limit));
  if (query) {
    params.set("q", query);
  }
  if (cursor) {
    params.set("cursor", cursor);
  }

  const data = await request<MessagePage>(`/api/messages?${params.toString()}`);
  return {
    messages: data.messages ?? [],
    nextCursor: data.nextCursor,
    hasMore: data.hasMore ?? false
  };
}

export async function fetchMessage(id: number): Promise<Message> {
  const data = await request<{ message: Message }>(`/api/messages/${id}`);
  return data.message;
}

export async function fetchStats(): Promise<Stats> {
  return request<Stats>("/api/stats");
}

export async function fetchAppInfo(): Promise<AppInfo> {
  return request<AppInfo>("/api/app");
}

export async function fetchSession(): Promise<SessionInfo> {
  const data = await request<{ session: SessionInfo }>("/api/session");
  return data.session;
}

export async function fetchSettings(): Promise<AppSettings> {
  const data = await request<{ settings: AppSettings }>("/api/settings");
  return data.settings;
}

export async function updateSettings(settings: AppSettings): Promise<AppSettings> {
  const data = await request<{ settings: AppSettings }>("/api/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ settings })
  });
  return data.settings;
}

export async function fetchAdminMailboxSettings(): Promise<AppSettings> {
  const data = await request<{ settings: AppSettings }>("/api/admin/mailbox-settings");
  return data.settings;
}

export async function updateAdminMailboxSettings(settings: AppSettings): Promise<AppSettings> {
  const data = await request<{ settings: AppSettings }>("/api/admin/mailbox-settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ settings })
  });
  return data.settings;
}

export async function deleteMessage(id: number): Promise<void> {
  await request<void>(`/api/messages/${id}`, { method: "DELETE" });
}

export async function clearInbox(): Promise<void> {
  await request<void>("/api/messages", { method: "DELETE" });
}

export async function fetchUsers(): Promise<User[]> {
  const data = await request<{ users: User[] }>("/api/admin/users");
  return data.users ?? [];
}

export async function createUser(payload: { username: string; password: string; settings: AppSettings }): Promise<User> {
  const data = await request<{ user: User }>("/api/admin/users", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  return data.user;
}

export async function updateUser(
  id: number,
  payload: { username: string; password: string; settings: AppSettings }
): Promise<User> {
  const data = await request<{ user: User }>(`/api/admin/users/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload)
  });
  return data.user;
}

export async function deleteUser(id: number): Promise<void> {
  await request<void>(`/api/admin/users/${id}`, { method: "DELETE" });
}

export async function logout(): Promise<void> {
  const response = await fetch("/auth/logout", withCSRF({ method: "POST" }));
  if (!response.ok) {
    if (response.status === 401) {
      window.location.href = "/login";
      return;
    }
    throw new Error(`Logout failed with status ${response.status}`);
  }
}

export function attachmentUrl(messageId: number, attachmentId: number): string {
  return `/api/messages/${messageId}/attachments/${attachmentId}`;
}

export function rawMessageUrl(messageId: number): string {
  return `/api/messages/${messageId}/raw`;
}

function withCSRF(init?: RequestInit): RequestInit | undefined {
  if (!init?.method || ["GET", "HEAD", "OPTIONS"].includes(init.method.toUpperCase())) {
    return init;
  }

  const headers = new Headers(init.headers ?? {});
  const token = readCookie("mailtail_csrf");
  if (token) {
    headers.set("X-CSRF-Token", token);
  }

  return {
    ...init,
    headers
  };
}

function readCookie(name: string): string {
  const prefix = `${name}=`;
  for (const part of document.cookie.split(";")) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) {
      return decodeURIComponent(trimmed.slice(prefix.length));
    }
  }
  return "";
}
