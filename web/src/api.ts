import type { Message, Stats } from "./types";

async function request<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init);
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

export async function fetchMessages(query: string): Promise<Message[]> {
  const url = query ? `/api/messages?q=${encodeURIComponent(query)}` : "/api/messages";
  const data = await request<{ messages: Message[] | null }>(url);
  return data.messages ?? [];
}

export async function fetchMessage(id: number): Promise<Message> {
  const data = await request<{ message: Message }>(`/api/messages/${id}`);
  return data.message;
}

export async function fetchStats(): Promise<Stats> {
  return request<Stats>("/api/stats");
}

export async function deleteMessage(id: number): Promise<void> {
  await request<void>(`/api/messages/${id}`, { method: "DELETE" });
}

export async function clearInbox(): Promise<void> {
  await request<void>("/api/messages", { method: "DELETE" });
}

export function attachmentUrl(messageId: number, attachmentId: number): string {
  return `/api/messages/${messageId}/attachments/${attachmentId}`;
}
