import { useEffect, useMemo, useState } from "react";
import {
  attachmentUrl,
  clearInbox,
  deleteMessage,
  fetchMessage,
  fetchMessages,
  fetchStats
} from "./api";
import type { Message, Stats } from "./types";

type TabKey = "html" | "text" | "headers" | "raw";

const tabs: Array<{ key: TabKey; label: string }> = [
  { key: "html", label: "HTML" },
  { key: "text", label: "Text" },
  { key: "headers", label: "Headers" },
  { key: "raw", label: "Raw" }
];

export function App() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selectedMessage, setSelectedMessage] = useState<Message | null>(null);
  const [stats, setStats] = useState<Stats>({ messageCount: 0, totalSize: 0 });
  const [query, setQuery] = useState("");
  const [activeTab, setActiveTab] = useState<TabKey>("html");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void loadOverview(query, selectedId);
    const timer = window.setInterval(() => {
      void loadOverview(query, selectedId, false);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [query, selectedId]);

  async function loadOverview(search: string, currentId: number | null, setBusy = true) {
    try {
      if (setBusy) {
        setLoading(true);
      }
      const [messageList, currentStats] = await Promise.all([
        fetchMessages(search),
        fetchStats()
      ]);
      setMessages(messageList);
      setStats(currentStats);

      const nextSelectedId = currentId ?? messageList[0]?.id ?? null;
      setSelectedId(nextSelectedId);

      if (nextSelectedId !== null) {
        const detail = await fetchMessage(nextSelectedId);
        setSelectedMessage(detail);
      } else {
        setSelectedMessage(null);
      }
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unknown error");
    } finally {
      setLoading(false);
    }
  }

  async function handleSelectMessage(id: number) {
    setSelectedId(id);
    try {
      const detail = await fetchMessage(id);
      setSelectedMessage(detail);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unknown error");
    }
  }

  async function handleDeleteCurrent() {
    if (selectedId === null) {
      return;
    }
    await deleteMessage(selectedId);
    await loadOverview(query, null);
  }

  async function handleClearInbox() {
    await clearInbox();
    await loadOverview("", null);
  }

  const content = useMemo(() => {
    if (!selectedMessage) {
      return {
        html: "<p>No message selected.</p>",
        text: "No message selected.",
        headers: "",
        raw: ""
      };
    }
    return {
      html: selectedMessage.htmlBody || "<p>No HTML part available.</p>",
      text: selectedMessage.textBody || "No text part available.",
      headers: (selectedMessage.headers ?? [])
        .map((header) => `${header.key}: ${header.value}`)
        .join("\n"),
      raw: selectedMessage.raw || ""
    };
  }, [selectedMessage]);

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div>
            <p className="eyebrow">SMTP Test Inbox</p>
            <h1>MailTail</h1>
          </div>
          <button className="ghostButton" onClick={() => void loadOverview(query, selectedId)}>
            Refresh
          </button>
        </div>

        <div className="statsCard">
          <div>
            <span>Stored messages</span>
            <strong>{stats.messageCount}</strong>
          </div>
          <div>
            <span>Total size</span>
            <strong>{formatBytes(stats.totalSize)}</strong>
          </div>
          <button className="dangerButton" onClick={() => void handleClearInbox()}>
            Clear inbox
          </button>
        </div>

        <label className="searchField">
          <span>Search</span>
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Subject, From, To"
          />
        </label>

        <div className="messageList">
          {messages.map((message) => (
            <button
              key={message.id}
              className={message.id === selectedId ? "messageItem active" : "messageItem"}
              onClick={() => void handleSelectMessage(message.id)}
            >
              <div className="messageTop">
                <strong>{message.subject || "(no subject)"}</strong>
                <span>{formatTime(message.receivedAt)}</span>
              </div>
              <span>{message.headerFrom || message.mailFrom}</span>
              <span className="mutedText">{message.headerTo || message.rcptTo.join(", ")}</span>
            </button>
          ))}
          {!loading && messages.length === 0 ? <p className="emptyState">No messages found.</p> : null}
        </div>
      </aside>

      <main className="contentPane">
        <div className="heroCard">
          <div>
            <p className="eyebrow">Current message</p>
            <h2>{selectedMessage?.subject || "Inbox is empty"}</h2>
            <div className="metaGrid">
              <span>From: {selectedMessage?.headerFrom || "-"}</span>
              <span>To: {selectedMessage?.headerTo || "-"}</span>
              <span>Received: {selectedMessage ? formatDate(selectedMessage.receivedAt) : "-"}</span>
              <span>Size: {selectedMessage ? formatBytes(selectedMessage.size) : "-"}</span>
              <span>HELO: {selectedMessage?.helo || "-"}</span>
              <span>Remote IP: {selectedMessage?.remoteIp || "-"}</span>
            </div>
          </div>

          <div className="heroActions">
            <button className="ghostButton" disabled={!selectedMessage} onClick={() => void handleDeleteCurrent()}>
              Delete message
            </button>
          </div>
        </div>

        <div className="tabs">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              className={activeTab === tab.key ? "tab active" : "tab"}
              onClick={() => setActiveTab(tab.key)}
            >
              {tab.label}
            </button>
          ))}
        </div>

        <section className="viewerCard">
          {activeTab === "html" ? (
            <iframe
              className="htmlFrame"
              title="HTML preview"
              sandbox=""
              srcDoc={content.html}
            />
          ) : (
            <pre className="codeBlock">{content[activeTab]}</pre>
          )}
        </section>

        <section className="attachmentsCard">
          <div className="attachmentsHeader">
            <h3>Attachments</h3>
            <span>{selectedMessage?.attachments?.length ?? 0}</span>
          </div>
          {selectedMessage?.attachments?.length ? (
            <div className="attachmentList">
              {selectedMessage.attachments.map((attachment) => (
                <a
                  key={attachment.id}
                  className="attachmentItem"
                  href={attachmentUrl(selectedMessage.id, attachment.id)}
                >
                  <strong>{attachment.fileName}</strong>
                  <span>
                    {attachment.contentType} · {formatBytes(attachment.size)}
                  </span>
                </a>
              ))}
            </div>
          ) : (
            <p className="emptyState">No attachments in this message.</p>
          )}
        </section>

        {error ? <div className="errorBanner">{error}</div> : null}
      </main>
    </div>
  );
}

function formatTime(value: string): string {
  return new Date(value).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit"
  });
}

function formatDate(value: string): string {
  return new Date(value).toLocaleString();
}

function formatBytes(size: number): string {
  if (size < 1024) {
    return `${size} B`;
  }
  if (size < 1024 * 1024) {
    return `${(size / 1024).toFixed(1)} KB`;
  }
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}
