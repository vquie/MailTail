import { useEffect, useMemo, useRef, useState } from "react";
import {
  attachmentUrl,
  clearInbox,
  deleteMessage,
  fetchAppInfo,
  fetchMessage,
  fetchMessages,
  fetchStats,
  logout
} from "./api";
import type { Message, Stats } from "./types";

type TabKey = "html" | "text" | "headers" | "raw";

const defaultPageSize = 25;

export function App() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selectedMessage, setSelectedMessage] = useState<Message | null>(null);
  const [stats, setStats] = useState<Stats>({ messageCount: 0, totalSize: 0 });
  const [version, setVersion] = useState("dev");
  const [query, setQuery] = useState("");
  const [queryInput, setQueryInput] = useState("");
  const [nextCursor, setNextCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [activeTab, setActiveTab] = useState<TabKey>("html");
  const [attachmentsExpanded, setAttachmentsExpanded] = useState(true);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [copiedHeaderKey, setCopiedHeaderKey] = useState<string | null>(null);
  const [copiedPaneKey, setCopiedPaneKey] = useState<TabKey | null>(null);
  const [error, setError] = useState<string | null>(null);
  const queryRef = useRef(query);
  const messagesRef = useRef(messages);
  const selectedIdRef = useRef<number | null>(selectedId);
  const selectedMessageRef = useRef<Message | null>(selectedMessage);
  const nextCursorRef = useRef(nextCursor);
  const hasMoreRef = useRef(hasMore);

  useEffect(() => {
    queryRef.current = query;
  }, [query]);

  useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);

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
    nextCursorRef.current = nextCursor;
  }, [nextCursor]);

  useEffect(() => {
    hasMoreRef.current = hasMore;
  }, [hasMore]);

  useEffect(() => {
    setAttachmentsExpanded(true);
  }, [selectedMessage?.id]);

  useEffect(() => {
    void loadOverview(query, {
      preferredId: selectedIdRef.current,
      forceDetail: true,
      resetList: true
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
      resetList?: boolean;
    } = {}
  ) {
    try {
      if (options.setBusy ?? true) {
        setLoading(true);
      }
      const resetList = options.resetList ?? false;
      const [page, currentStats, appInfo] = await Promise.all([
        fetchMessages(search, "", defaultPageSize),
        fetchStats(),
        fetchAppInfo()
      ]);
      const loadedMore = messagesRef.current.length > page.messages.length;
      const messageList = resetList ? page.messages : mergeMessages(page.messages, messagesRef.current);
      setMessages(messageList);
      setStats(currentStats);
      setVersion(appInfo.version);
      setNextCursor(resetList || !loadedMore ? page.nextCursor ?? "" : nextCursorRef.current);
      setHasMore(resetList || !loadedMore ? page.hasMore : hasMoreRef.current);

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

  async function handleLoadMore() {
    if (!nextCursorRef.current || loadingMore) {
      return;
    }

    try {
      setLoadingMore(true);
      const page = await fetchMessages(queryRef.current, nextCursorRef.current, defaultPageSize);
      setMessages((current) => mergeMessages(current, page.messages));
      setNextCursor(page.nextCursor ?? "");
      setHasMore(page.hasMore);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unknown error");
    } finally {
      setLoadingMore(false);
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
    await loadOverview(queryRef.current, { preferredId: null, forceDetail: true, resetList: true });
  }

  async function handleClearInbox() {
    await clearInbox();
    setQueryInput("");
    setQuery("");
    await loadOverview("", { preferredId: null, forceDetail: true, resetList: true });
  }

  async function handleLogout() {
    await logout();
    window.location.href = "/login";
  }

  async function handleCopyHeaderValue(headerKey: string, value: string) {
    try {
      await writeClipboard(value);
      setCopiedHeaderKey(headerKey);
      window.setTimeout(() => {
        setCopiedHeaderKey((current) => (current === headerKey ? null : current));
      }, 1500);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to copy header");
    }
  }

  async function handleCopyCurrentPane() {
    const paneValue = currentPaneCopyValue(activeTab, selectedMessage);
    if (!paneValue) {
      return;
    }

    try {
      await writeClipboard(paneValue);
      setCopiedPaneKey(activeTab);
      window.setTimeout(() => {
        setCopiedPaneKey((current) => (current === activeTab ? null : current));
      }, 1500);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to copy content");
    }
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
  const paneCopyValue = currentPaneCopyValue(activeTab, selectedMessage);
  const paneCopyLabel = activeTab === "headers" ? "Copy all" : "Copy all";

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
            <span>{messages.length} loaded</span>
            <span>{hasMore ? "More available" : selectedMessage ? `#${selectedMessage.id}` : "No selection"}</span>
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
          {hasMore ? (
            <button className="ghostButton compactButton loadMoreButton" disabled={loadingMore} onClick={() => void handleLoadMore()}>
              {loadingMore ? "Loading..." : "Load more"}
            </button>
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
            onClick={() =>
              void loadOverview(queryRef.current, {
                preferredId: selectedIdRef.current,
                forceDetail: true
              })
            }
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
                  <button
                    className="ghostButton compactButton viewerCopyButton"
                    disabled={!paneCopyValue}
                    onClick={() => void handleCopyCurrentPane()}
                  >
                    {copiedPaneKey === activeTab ? <CheckIcon /> : <CopyIcon />}
                    <span>{copiedPaneKey === activeTab ? "Copied" : paneCopyLabel}</span>
                  </button>
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
                      <>
                        {selectedMessage.headers?.map((header, index) => {
                          const headerCopyKey = `${header.key}-${index}`;
                          return (
                            <div key={headerCopyKey} className="headerRow">
                              <span className="headerKey">{header.key}</span>
                              <div className="headerValueGroup">
                                <span className="headerValue">{header.value}</span>
                                <button
                                  className={copiedHeaderKey === headerCopyKey ? "ghostButton compactButton iconButton copied" : "ghostButton compactButton iconButton"}
                                  aria-label={`Copy ${header.key} header`}
                                  title={copiedHeaderKey === headerCopyKey ? "Copied" : `Copy ${header.key}`}
                                  onClick={() => void handleCopyHeaderValue(headerCopyKey, header.value)}
                                >
                                  {copiedHeaderKey === headerCopyKey ? <CheckIcon /> : <CopyIcon />}
                                </button>
                              </div>
                            </div>
                          );
                        })}
                      </>
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

function mergeMessages(primary: Message[], secondary: Message[]): Message[] {
  const merged = new Map<number, Message>();
  for (const message of primary) {
    merged.set(message.id, message);
  }
  for (const message of secondary) {
    if (!merged.has(message.id)) {
      merged.set(message.id, message);
    }
  }
  return Array.from(merged.values());
}

function currentPaneCopyValue(activeTab: TabKey, message: Message | null): string {
  if (!message) {
    return "";
  }

  switch (activeTab) {
    case "html":
      return message.htmlBody ?? "";
    case "text":
      return message.textBody ?? "";
    case "headers":
      return (message.headers ?? []).map((header) => `${header.key}: ${header.value}`).join("\n");
    case "raw":
      return message.raw ?? "";
    default:
      return "";
  }
}

async function writeClipboard(value: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(value);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "absolute";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.select();

  try {
    document.execCommand("copy");
  } finally {
    document.body.removeChild(textarea);
  }
}

function CopyIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="6" y="3" width="7" height="9" rx="1.5" />
      <path d="M4.5 5H4a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h5a1 1 0 0 0 1-1v-.5" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3.5 8.5 6.5 11.5 12.5 4.5" />
    </svg>
  );
}
