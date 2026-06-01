import { useEffect, useMemo, useRef, useState } from "react";
import {
  attachmentUrl,
  fetchAppInfo,
  clearInbox,
  deleteMessage,
  fetchMessage,
  fetchMessages,
  fetchStats,
  logout
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
  const [version, setVersion] = useState("dev");
  const [query, setQuery] = useState("");
  const [queryInput, setQueryInput] = useState("");
  const [activeTab, setActiveTab] = useState<TabKey>("html");
  const [attachmentsExpanded, setAttachmentsExpanded] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const queryRef = useRef(query);
  const selectedIdRef = useRef<number | null>(selectedId);
  const selectedMessageRef = useRef<Message | null>(selectedMessage);

  useEffect(() => {
    queryRef.current = query;
  }, [query]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setQuery(queryInput.trim());
    }, 150);
    return () => window.clearTimeout(timer);
  }, [queryInput]);

  useEffect(() => {
    selectedIdRef.current = selectedId;
  }, [selectedId]);

  useEffect(() => {
    selectedMessageRef.current = selectedMessage;
  }, [selectedMessage]);

  useEffect(() => {
    setAttachmentsExpanded(true);
  }, [selectedMessage?.id]);

  useEffect(() => {
    void loadOverview(query, {
      preferredId: selectedIdRef.current,
      forceDetail: true
    });

    const timer = window.setInterval(() => {
      if (document.visibilityState !== "visible") {
        return;
      }

      void loadOverview(queryRef.current, {
        preferredId: selectedIdRef.current,
        setBusy: false
      });
    }, 15000);

    return () => window.clearInterval(timer);
  }, [query]);

  async function loadOverview(
    search: string,
    options: {
      preferredId?: number | null;
      forceDetail?: boolean;
      setBusy?: boolean;
    } = {}
  ) {
    try {
      if (options.setBusy ?? true) {
        setLoading(true);
      }
      const [messageList, currentStats, appInfo] = await Promise.all([
        fetchMessages(search),
        fetchStats(),
        fetchAppInfo()
      ]);
      setMessages(messageList);
      setStats(currentStats);
      setVersion(appInfo.version);

      const preferredId = options.preferredId ?? selectedIdRef.current;
      const nextSelectedId =
        preferredId !== null && messageList.some((message) => message.id === preferredId)
          ? preferredId
          : messageList[0]?.id ?? null;
      const selectionChanged = nextSelectedId !== selectedIdRef.current;

      setSelectedId(nextSelectedId);

      if (nextSelectedId !== null) {
        const shouldFetchDetail =
          options.forceDetail ||
          selectionChanged ||
          selectedMessageRef.current?.id !== nextSelectedId;

        if (!shouldFetchDetail) {
          setError(null);
          return;
        }

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
    await loadOverview(queryRef.current, { preferredId: null, forceDetail: true });
  }

  async function handleClearInbox() {
    await clearInbox();
    setQueryInput("");
    setQuery("");
    await loadOverview("", { preferredId: null, forceDetail: true });
  }

  async function handleLogout() {
    await logout();
    window.location.href = "/login";
  }

  const content = useMemo(() => {
    if (!selectedMessage) {
      return {
        html: "<p>No message selected.</p>",
        text: "No message selected.",
        raw: ""
      };
    }
    return {
      html: selectedMessage.htmlBody || "<p>No HTML part available.</p>",
      text: selectedMessage.textBody || "No text part available.",
      raw: selectedMessage.raw || ""
    };
  }, [selectedMessage]);

  const availableTabs = useMemo<Array<{ key: TabKey; label: string }>>(() => {
    const hasHTML = Boolean(selectedMessage?.htmlBody?.trim());
    const hasText = Boolean(selectedMessage?.textBody?.trim());

    const contentTabs: Array<{ key: TabKey; label: string }> = [];
    if (hasHTML) {
      contentTabs.push({ key: "html", label: "HTML" });
    }
    if (hasText) {
      contentTabs.push({ key: "text", label: "Text" });
    }
    if (!hasHTML && !hasText) {
      contentTabs.push({ key: "text", label: "Text" });
    }

    return [
      ...contentTabs,
      { key: "headers", label: "Headers" },
      { key: "raw", label: "Raw" }
    ];
  }, [selectedMessage]);

  useEffect(() => {
    if (availableTabs.some((tab) => tab.key === activeTab)) {
      return;
    }
    setActiveTab(availableTabs[0]?.key ?? "text");
  }, [activeTab, availableTabs]);

  const hasMessages = messages.length > 0;
  const hasAttachments = Boolean(selectedMessage?.attachments?.length);
  const smtpExampleRecipient = "test@example.test";
  const smtpExampleSender = "sender@example.test";

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div>
            <p className="eyebrow">SMTP Test Inbox</p>
            <h1>MailTail</h1>
          </div>
        </div>

        <div className="statsRow">
          <div className="statPill">
            <span>Messages</span>
            <strong>{stats.messageCount}</strong>
          </div>
          <div className="statPill">
            <span>Size</span>
            <strong>{formatBytes(stats.totalSize)}</strong>
          </div>
        </div>

        <div className="searchCard">
          <label className="searchField">
            <span>Search</span>
            <input
              value={queryInput}
              onChange={(event) => setQueryInput(event.target.value)}
              placeholder="Subject, From, To"
            />
          </label>
          <div className="listMeta">
            <span>{messages.length} visible</span>
            <span>{selectedMessage ? `#${selectedMessage.id}` : "No selection"}</span>
          </div>
        </div>

        <div className="messageList">
          {messages.map((message) => (
            <button
              key={message.id}
              className={message.id === selectedId ? "messageItem active" : "messageItem"}
              onClick={() => void handleSelectMessage(message.id)}
            >
              <div className="messageTop">
                <strong className="messageSubject">{message.subject || "(no subject)"}</strong>
                <span className="messageTime">{formatTime(message.receivedAt)}</span>
              </div>
              <div className="messageMeta">
                <span className="messageMetaLabel">From</span>
                <span className="messageLine">{message.headerFrom || message.mailFrom}</span>
              </div>
              <div className="messageMeta">
                <span className="messageMetaLabel">Rcpt</span>
                <span className="mutedText messageLine">{message.headerTo || message.rcptTo.join(", ")}</span>
              </div>
            </button>
          ))}
          {!loading && messages.length === 0 ? (
            <div className="emptyListCard">
              <strong>Inbox is empty</strong>
              <p>No messages captured yet.</p>
            </div>
          ) : null}
        </div>

        <div className="sidebarVersion">Version {version}</div>
      </aside>

      <main className="contentPane">
        <div className="topToolbar">
          <button className="ghostButton compactButton" onClick={() => void handleLogout()}>
            Logout
          </button>
          <button
            className="ghostButton compactButton"
            onClick={() => void loadOverview(queryRef.current, { preferredId: selectedIdRef.current, forceDetail: true })}
          >
            Refresh
          </button>
          <button className="dangerButton compactButton" disabled={!hasMessages} onClick={() => void handleClearInbox()}>
            Clear all messages
          </button>
        </div>

        {selectedMessage ? (
          <section className="messageWorkspace">
            <div className="heroCard compactHero">
              <div className="heroCopy">
                <p className="eyebrow">Current message</p>
                <h2>{selectedMessage.subject}</h2>
                <div className="metaGrid">
                  <span>From: {selectedMessage.headerFrom || "-"}</span>
                  <span>To: {selectedMessage.headerTo || "-"}</span>
                  <span>Received: {formatDate(selectedMessage.receivedAt)}</span>
                  <span>Size: {formatBytes(selectedMessage.size)}</span>
                  <span>HELO: {selectedMessage.helo || "-"}</span>
                  <span>Remote IP: {selectedMessage.remoteIp || "-"}</span>
                </div>
              </div>

              <div className="heroActions">
                <button className="ghostButton compactButton" onClick={() => void handleDeleteCurrent()}>
                  Delete message
                </button>
              </div>
            </div>

            <div className={hasAttachments && attachmentsExpanded ? "detailGrid hasAttachments" : "detailGrid"}>
              <section className="viewerCard">
                <div className="viewerHeader">
                  <div className="tabs">
                    {availableTabs.map((tab) => (
                      <button
                        key={tab.key}
                        className={activeTab === tab.key ? "tab active" : "tab"}
                        onClick={() => setActiveTab(tab.key)}
                      >
                        {tab.label}
                      </button>
                    ))}
                  </div>
                </div>
                {activeTab === "html" ? (
                  <iframe
                    className="htmlFrame"
                    title="HTML preview"
                    sandbox=""
                    srcDoc={content.html}
                  />
                ) : activeTab === "headers" ? (
                  <div className="headersList">
                    {(selectedMessage.headers ?? []).length ? (
                      selectedMessage.headers?.map((header, index) => (
                        <div key={`${header.key}-${index}`} className="headerRow">
                          <span className="headerKey">{header.key}</span>
                          <span className="headerValue">{header.value}</span>
                        </div>
                      ))
                    ) : (
                      <p className="emptyState">No headers available.</p>
                    )}
                  </div>
                ) : (
                  <pre className="codeBlock">{content[activeTab]}</pre>
                )}
              </section>

              {hasAttachments ? (
                <section className={attachmentsExpanded ? "attachmentsCard" : "attachmentsToggleCard"}>
                  <div className="attachmentsHeader">
                    <h3>Attachments</h3>
                    <div className="attachmentsHeaderActions">
                      <span>{selectedMessage.attachments?.length ?? 0}</span>
                      <button
                        className="ghostButton compactButton attachmentsToggle"
                        onClick={() => setAttachmentsExpanded((current) => !current)}
                      >
                        {attachmentsExpanded ? "Collapse" : "Expand"}
                      </button>
                    </div>
                  </div>
                  {attachmentsExpanded ? (
                    <div className="attachmentList">
                      {selectedMessage.attachments?.map((attachment) => (
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
                  ) : null}
                </section>
              ) : null}
            </div>
          </section>
        ) : (
          <section className="emptyHero">
            <div className="emptyHeroMain">
              <p className="eyebrow">Inbox state</p>
              <h2>Inbox is empty</h2>
              <p className="emptyCopy">
                Inject a test email over SMTP on port 8025. The first captured message will open here with HTML, text,
                headers, raw source and attachments.
              </p>
              <pre className="commandSnippet">{`swaks -s 127.0.0.1:8025 --to ${smtpExampleRecipient} --from ${smtpExampleSender}`}</pre>
            </div>
            <div className="heroActions">
            </div>
          </section>
        )}

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
