import { useEffect, useMemo, useRef, useState } from "react";
import {
  attachmentUrl,
  clearInbox,
  createUser,
  deleteMessage,
  deleteUser,
  fetchAppInfo,
  fetchAdminMailboxSettings,
  fetchMessage,
  fetchMessages,
  fetchSettings,
  fetchStats,
  fetchSession,
  fetchUsers,
  rawMessageUrl,
  logout,
  updateAdminMailboxSettings,
  updateSettings,
  updateUser
} from "./api";
import type { AppSettings, Message, SessionInfo, Stats, User } from "./types";

type TabKey = "html" | "text" | "headers" | "raw";

const defaultPageSize = 25;
const defaultMailFailRulesFile = "examples/mailfail.yaml";
const defaultAutoDeleteDays = 30;
const emptySettings: AppSettings = {
  allowedOrigins: "",
  smtpLogVerbose: false,
  mailFailEnabled: false,
  mailFailRulesFile: defaultMailFailRulesFile,
  allowedRemoteIps: "",
  acceptedRcptDomains: "",
  acceptedFromDomains: "",
  autoDeleteAfterDays: 0
};

const emptyManagedUser = {
  username: "",
  password: ""
};

export function App() {
  const [messages, setMessages] = useState<Message[]>([]);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [selectedMessage, setSelectedMessage] = useState<Message | null>(null);
  const [stats, setStats] = useState<Stats>({ messageCount: 0, totalSize: 0 });
  const [version, setVersion] = useState("");
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [currentSettings, setCurrentSettings] = useState<AppSettings>(emptySettings);
  const [globalSettingsDraft, setGlobalSettingsDraft] = useState<AppSettings>(emptySettings);
  const [adminMailboxDraft, setAdminMailboxDraft] = useState<AppSettings>(emptySettings);
  const [adminMailboxEnabled, setAdminMailboxEnabled] = useState(false);
  const [adminMailboxLimitRemoteIps, setAdminMailboxLimitRemoteIps] = useState(false);
  const [adminMailboxLimitAcceptedRcptDomains, setAdminMailboxLimitAcceptedRcptDomains] = useState(false);
  const [adminMailboxLimitAcceptedFromDomains, setAdminMailboxLimitAcceptedFromDomains] = useState(false);
  const [adminMailboxAutoDeleteEnabled, setAdminMailboxAutoDeleteEnabled] = useState(false);
  const [settingsLoaded, setSettingsLoaded] = useState(false);
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
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsDraft, setSettingsDraft] = useState<AppSettings>(emptySettings);
  const [settingsLoading, setSettingsLoading] = useState(false);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [settingsNotice, setSettingsNotice] = useState<string | null>(null);
  const [limitAllowedRemoteIps, setLimitAllowedRemoteIps] = useState(false);
  const [limitAcceptedRcptDomains, setLimitAcceptedRcptDomains] = useState(false);
  const [limitAcceptedFromDomains, setLimitAcceptedFromDomains] = useState(false);
  const [autoDeleteEnabled, setAutoDeleteEnabled] = useState(false);
  const [managedUsers, setManagedUsers] = useState<User[]>([]);
  const [selectedManagedUserId, setSelectedManagedUserId] = useState<number | null>(null);
  const [createUserOpen, setCreateUserOpen] = useState(false);
  const [managedUsername, setManagedUsername] = useState(emptyManagedUser.username);
  const [managedPassword, setManagedPassword] = useState(emptyManagedUser.password);
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
    void loadSessionInfo();
  }, []);

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

  async function loadCurrentSettings() {
    try {
      const settings = await fetchSettings();
      setCurrentSettings(normalizeSettingsDraft(settings));
      setSettingsLoaded(true);
    } catch {
      setSettingsLoaded(false);
    }
  }

  async function loadSessionInfo() {
    try {
      const currentSession = await fetchSession();
      setSession(currentSession);
      if (!currentSession.isAdmin) {
        await loadCurrentSettings();
      } else {
        setSettingsLoaded(false);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load session");
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

  async function openSettingsPanel() {
    try {
      setSettingsOpen(true);
      setSettingsLoading(true);
      setSettingsError(null);
      setSettingsNotice(null);
      if (session?.isAdmin) {
        const globalSettings = await fetchSettings();
        const adminMailboxSettings = await fetchAdminMailboxSettings();
        const users = await fetchUsers();
        setGlobalSettingsDraft(normalizeSettingsDraft(globalSettings));
        const normalizedAdminMailbox = normalizeSettingsDraft(adminMailboxSettings);
        setAdminMailboxDraft(normalizedAdminMailbox);
        applyAdminMailboxToggles(normalizedAdminMailbox);
        setManagedUsers(users);
        if (users.length > 0) {
          applyManagedUserDraft(users[0]);
          setSelectedManagedUserId(users[0].id);
        } else {
          applyManagedUserDraft(null);
          setSelectedManagedUserId(null);
        }
        return;
      }

      const settings = await fetchSettings();
      const normalized = normalizeSettingsDraft(settings);
      setSettingsDraft(normalized);
      setCurrentSettings(normalized);
      setSettingsLoaded(true);
      applySettingsToggles(normalized);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to load settings");
    } finally {
      setSettingsLoading(false);
    }
  }

  async function handleSaveSettings() {
    if (session?.isAdmin) {
      await handleSaveManagedUser();
      return;
    }

    try {
      setSettingsSaving(true);
      const saved = await updateSettings(buildSettingsPayload(settingsDraft, {
        limitAllowedRemoteIps,
        limitAcceptedRcptDomains,
        limitAcceptedFromDomains,
        autoDeleteEnabled
      }));
      const normalized = normalizeSettingsDraft(saved);
      setSettingsDraft(normalized);
      setCurrentSettings(normalized);
      setSettingsLoaded(true);
      setSettingsNotice("Saved. Changes are applied immediately.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to save settings");
    } finally {
      setSettingsSaving(false);
    }
  }

  function applySettingsToggles(settings: AppSettings) {
    setLimitAllowedRemoteIps(Boolean(settings.allowedRemoteIps.trim()));
    setLimitAcceptedRcptDomains(Boolean(settings.acceptedRcptDomains.trim()));
    setLimitAcceptedFromDomains(Boolean(settings.acceptedFromDomains.trim()));
    setAutoDeleteEnabled(settings.autoDeleteAfterDays > 0);
  }

  function applyManagedUserDraft(user: User | null) {
    const normalized = normalizeSettingsDraft(user?.settings ?? emptySettings);
    setSettingsDraft(normalized);
    setManagedUsername(user?.username ?? "");
    setManagedPassword("");
    applySettingsToggles(normalized);
  }

  async function handleSaveManagedUser() {
    try {
      setSettingsSaving(true);
      const payload = {
        username: managedUsername.trim(),
        password: managedPassword,
        settings: buildSettingsPayload(settingsDraft, {
          limitAllowedRemoteIps,
          limitAcceptedRcptDomains,
          limitAcceptedFromDomains,
          autoDeleteEnabled
        })
      };

      const savedUser = selectedManagedUserId
        ? await updateUser(selectedManagedUserId, payload)
        : await createUser(payload);

      const users = await fetchUsers();
      setManagedUsers(users);
      setSelectedManagedUserId(savedUser.id);
      applyManagedUserDraft(savedUser);
      setSettingsNotice(selectedManagedUserId ? "User updated." : "User created.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to save user");
    } finally {
      setSettingsSaving(false);
    }
  }

  async function handleSaveGlobalSettings() {
    try {
      setSettingsSaving(true);
      const saved = await updateSettings({
        ...emptySettings,
        allowedOrigins: globalSettingsDraft.allowedOrigins,
        smtpLogVerbose: globalSettingsDraft.smtpLogVerbose
      });
      setGlobalSettingsDraft(normalizeSettingsDraft(saved));
      setSettingsNotice("Platform settings saved.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to save platform settings");
    } finally {
      setSettingsSaving(false);
    }
  }

  function applyAdminMailboxToggles(settings: AppSettings) {
    setAdminMailboxEnabled(
      Boolean(
        settings.mailFailEnabled ||
          settings.allowedRemoteIps.trim() ||
          settings.acceptedRcptDomains.trim() ||
          settings.acceptedFromDomains.trim() ||
          settings.autoDeleteAfterDays > 0
      )
    );
    setAdminMailboxLimitRemoteIps(Boolean(settings.allowedRemoteIps.trim()));
    setAdminMailboxLimitAcceptedRcptDomains(Boolean(settings.acceptedRcptDomains.trim()));
    setAdminMailboxLimitAcceptedFromDomains(Boolean(settings.acceptedFromDomains.trim()));
    setAdminMailboxAutoDeleteEnabled(settings.autoDeleteAfterDays > 0);
  }

  async function handleSaveAdminMailboxSettings() {
    try {
      setSettingsSaving(true);
      const saved = await updateAdminMailboxSettings(
        adminMailboxEnabled
          ? buildSettingsPayload(adminMailboxDraft, {
              limitAllowedRemoteIps: adminMailboxLimitRemoteIps,
              limitAcceptedRcptDomains: adminMailboxLimitAcceptedRcptDomains,
              limitAcceptedFromDomains: adminMailboxLimitAcceptedFromDomains,
              autoDeleteEnabled: adminMailboxAutoDeleteEnabled
            })
          : emptySettings
      );
      const normalized = normalizeSettingsDraft(saved);
      setAdminMailboxDraft(normalized);
      applyAdminMailboxToggles(normalized);
      setSettingsNotice("Admin mailbox settings saved.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to save admin mailbox settings");
    } finally {
      setSettingsSaving(false);
    }
  }

  async function handleCreateUserWithCredentials() {
    try {
      setSettingsSaving(true);
      const savedUser = await createUser({
        username: managedUsername.trim(),
        password: managedPassword,
        settings: emptySettings
      });

      const users = await fetchUsers();
      setManagedUsers(users);
      setSelectedManagedUserId(savedUser.id);
      applyManagedUserDraft(savedUser);
      setCreateUserOpen(false);
      setSettingsNotice("User created. Configure delivery policies on the right.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to create user");
    } finally {
      setSettingsSaving(false);
    }
  }

  async function handleDeleteManagedUser() {
    if (!selectedManagedUserId) {
      return;
    }
    try {
      setSettingsSaving(true);
      await deleteUser(selectedManagedUserId);
      const users = await fetchUsers();
      setManagedUsers(users);
      if (users.length > 0) {
        setSelectedManagedUserId(users[0].id);
        applyManagedUserDraft(users[0]);
      } else {
        setSelectedManagedUserId(null);
        applyManagedUserDraft(null);
      }
      setSettingsNotice("User deleted.");
      setSettingsError(null);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "Failed to delete user");
    } finally {
      setSettingsSaving(false);
    }
  }

  function handleCreateManagedUser() {
    setCreateUserOpen(true);
    setSelectedManagedUserId(null);
    applyManagedUserDraft(null);
    setSettingsNotice(null);
    setSettingsError(null);
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

  function updateSettingsField<K extends keyof AppSettings>(key: K, value: AppSettings[K]) {
    setSettingsDraft((current) => ({
      ...current,
      [key]: value
    }));
    setSettingsNotice(null);
  }

  function handleToggleMailFail(enabled: boolean) {
    updateSettingsField("mailFailEnabled", enabled);
    if (enabled && !settingsDraft.mailFailRulesFile.trim()) {
      updateSettingsField("mailFailRulesFile", defaultMailFailRulesFile);
    }
  }

  function handleToggleAutoDelete(enabled: boolean) {
    setAutoDeleteEnabled(enabled);
    if (enabled && settingsDraft.autoDeleteAfterDays <= 0) {
      updateSettingsField("autoDeleteAfterDays", defaultAutoDeleteDays);
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
      html: prepareHtmlPreview(selectedMessage.htmlBody || "<p>No HTML part available.</p>"),
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
  const hasActiveSearch = query.length > 0;

  return (
    <div className="shell">
      <header className="workspaceHeader">
        <div className="workspaceHeaderCard">
          <div className="workspaceTopRow">
            <div className="workspaceTitleRow">
              <div className="workspaceTitle">
                <p className="eyebrow">SMTP Test Inbox</p>
                <h1>MailTail</h1>
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
            </div>

            <label className="searchField workspaceSearchField">
              <input
                aria-label="Search subject, from, to"
                value={queryInput}
                onChange={(event) => setQueryInput(event.target.value)}
                placeholder="Search subject, from, to"
              />
            </label>

            <div className="topToolbar">
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
              <button className="ghostButton compactButton" onClick={() => void openSettingsPanel()}>
                Settings
              </button>
              <button className="dangerButton compactButton" disabled={!hasMessages} onClick={() => void handleClearInbox()}>
                Clear all messages
              </button>
              <button className="ghostButton compactButton" onClick={() => void handleLogout()}>
                Logout
              </button>
            </div>
          </div>
        </div>
      </header>

      <aside className="sidebar">
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
              <strong>{hasActiveSearch ? "No matching messages" : "Inbox is empty"}</strong>
              <p>{hasActiveSearch ? `No results for "${query}".` : "No messages captured yet."}</p>
            </div>
          ) : null}
          {hasMore ? (
            <button className="ghostButton compactButton loadMoreButton" disabled={loadingMore} onClick={() => void handleLoadMore()}>
              {loadingMore ? "Loading..." : "Load more"}
            </button>
          ) : null}
        </div>

        <div className="listMeta sidebarListMeta">
          <span>{messages.length} loaded</span>
          <span>{hasMore ? "More available" : selectedMessage ? `#${selectedMessage.id}` : "No selection"}</span>
        </div>

        <div className="sidebarFooter">
          {!session?.isAdmin && settingsLoaded && currentSettings.mailFailEnabled ? (
            <div className="sidebarNotice">
              <span className="statusDot" aria-hidden="true" />
              <span>MailFail enabled for this user</span>
            </div>
          ) : null}
          <div className="sidebarVersion">{version ? `Version ${version}` : "\u00A0"}</div>
        </div>
      </aside>

      <main className="contentPane">
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
                <a
                  className="ghostButton compactButton toolbarLink"
                  href={rawMessageUrl(selectedMessage.id)}
                  download={buildEMLFileName(selectedMessage)}
                >
                  Download EML
                </a>
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
                                <button
                                  className={copiedHeaderKey === headerCopyKey ? "ghostButton compactButton iconButton copied" : "ghostButton compactButton iconButton"}
                                  aria-label={`Copy ${header.key} header`}
                                  title={copiedHeaderKey === headerCopyKey ? "Copied" : `Copy ${header.key}`}
                                  onClick={() => void handleCopyHeaderValue(headerCopyKey, header.value)}
                                >
                                  {copiedHeaderKey === headerCopyKey ? <CheckIcon /> : <CopyIcon />}
                                </button>
                                <span className="headerValue">{header.value}</span>
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

        {settingsOpen ? (
          <div className="settingsOverlay" onClick={() => setSettingsOpen(false)}>
            <section className="settingsPanel" onClick={(event) => event.stopPropagation()}>
              <div className="settingsPanelHeader">
                <div>
                  <p className="eyebrow">{session?.isAdmin ? "Admin area" : "User settings"}</p>
                  <h2>{session?.isAdmin ? "Users" : "Settings"}</h2>
                </div>
                <button className="ghostButton compactButton" onClick={() => setSettingsOpen(false)}>
                  Close
                </button>
              </div>

              <p className="settingsLead">
                {session?.isAdmin
                  ? "Manage local users and their delivery policies."
                  : "These settings are persisted in SQLite and applied live without a restart."}
              </p>

              {settingsLoading ? <p className="emptyState">Loading settings...</p> : null}
              {settingsError ? <div className="errorBanner settingsBanner">{settingsError}</div> : null}
              {settingsNotice ? <div className="settingsNotice">{settingsNotice}</div> : null}

              {!settingsLoading ? (
                session?.isAdmin ? (
                  <div className="adminSettingsLayout">
                    <section className="settingsField platformSettingsCard">
                      <div className="adminSectionHeader">
                        <span>Platform settings</span>
                      </div>
                      <div className="platformSettingsRow">
                        <label className="settingsField nestedSettingsField">
                          <span>Allowed origins</span>
                          <textarea
                            rows={3}
                            value={globalSettingsDraft.allowedOrigins}
                            onChange={(event) =>
                              setGlobalSettingsDraft((current) => ({
                                ...current,
                                allowedOrigins: event.target.value
                              }))
                            }
                          />
                          <small>Instance-wide CORS allow-list. Applies to all users.</small>
                        </label>
                        <div className="settingsField nestedSettingsField platformToggleCard">
                          <span>Verbose SMTP logging</span>
                          <label className="toggleRow">
                            <input
                              type="checkbox"
                              checked={globalSettingsDraft.smtpLogVerbose}
                              onChange={(event) =>
                                setGlobalSettingsDraft((current) => ({
                                  ...current,
                                  smtpLogVerbose: event.target.checked
                                }))
                              }
                            />
                            <span>Enable verbose SMTP logging for the whole instance</span>
                          </label>
                          <small>Applies to all SMTP sessions on this MailTail instance.</small>
                        </div>
                      </div>
                      <div className="platformSettingsActions">
                        <button className="ghostButton compactButton" disabled={settingsSaving} onClick={() => void handleSaveGlobalSettings()}>
                          Save platform settings
                        </button>
                      </div>
                    </section>

                    <section className="settingsField platformSettingsCard">
                      <div className="adminSectionHeader">
                        <div>
                          <span>Admin mailbox</span>
                          <p className="adminEditorLead">Use this if the env admin should also receive and retain mail.</p>
                        </div>
                        <button className="ghostButton compactButton" disabled={settingsSaving} onClick={() => void handleSaveAdminMailboxSettings()}>
                          Save admin mailbox
                        </button>
                      </div>

                      <label className="toggleRow">
                        <input
                          type="checkbox"
                          checked={adminMailboxEnabled}
                          onChange={(event) => setAdminMailboxEnabled(event.target.checked)}
                        />
                        <span>Enable admin mailbox policies</span>
                      </label>

                      {adminMailboxEnabled ? (
                        <div className="settingsGrid adminMailboxGrid">
                          <div className="settingsField toggleField">
                            <span>MailFail enabled</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={adminMailboxDraft.mailFailEnabled}
                                onChange={(event) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    mailFailEnabled: event.target.checked,
                                    mailFailRulesFile:
                                      event.target.checked && !current.mailFailRulesFile.trim()
                                        ? defaultMailFailRulesFile
                                        : current.mailFailRulesFile
                                  }))
                                }
                              />
                              <span>Enable MailFail rule evaluation</span>
                            </label>
                          </div>

                          {adminMailboxDraft.mailFailEnabled ? (
                            <label className="settingsField">
                              <span>MailFail rules file</span>
                              <input
                                value={adminMailboxDraft.mailFailRulesFile}
                                onChange={(event) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    mailFailRulesFile: event.target.value
                                  }))
                                }
                                placeholder={defaultMailFailRulesFile}
                              />
                              <small>Defaults to {defaultMailFailRulesFile} unless you override it.</small>
                            </label>
                          ) : null}

                          <div className="settingsField toggleField">
                            <span>Allowed remote IPs</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={adminMailboxLimitRemoteIps}
                                onChange={(event) => setAdminMailboxLimitRemoteIps(event.target.checked)}
                              />
                              <span>Restrict SMTP connections to specific IPs or CIDRs</span>
                            </label>
                            {adminMailboxLimitRemoteIps ? (
                              <textarea
                                rows={3}
                                value={adminMailboxDraft.allowedRemoteIps}
                                onChange={(event) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    allowedRemoteIps: event.target.value
                                  }))
                                }
                              />
                            ) : null}
                          </div>

                          <div className="settingsField toggleField">
                            <span>Accepted recipient domains</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={adminMailboxLimitAcceptedRcptDomains}
                                onChange={(event) => setAdminMailboxLimitAcceptedRcptDomains(event.target.checked)}
                              />
                              <span>Restrict accepted recipient domains</span>
                            </label>
                            {adminMailboxLimitAcceptedRcptDomains ? (
                              <textarea
                                rows={3}
                                value={adminMailboxDraft.acceptedRcptDomains}
                                onChange={(event) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    acceptedRcptDomains: event.target.value
                                  }))
                                }
                              />
                            ) : null}
                          </div>

                          <div className="settingsField toggleField">
                            <span>Accepted sender domains</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={adminMailboxLimitAcceptedFromDomains}
                                onChange={(event) => setAdminMailboxLimitAcceptedFromDomains(event.target.checked)}
                              />
                              <span>Restrict accepted sender domains</span>
                            </label>
                            {adminMailboxLimitAcceptedFromDomains ? (
                              <textarea
                                rows={3}
                                value={adminMailboxDraft.acceptedFromDomains}
                                onChange={(event) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    acceptedFromDomains: event.target.value
                                  }))
                                }
                              />
                            ) : null}
                          </div>

                          <div className="settingsField toggleField">
                            <span>Automatic message deletion</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={adminMailboxAutoDeleteEnabled}
                                onChange={(event) => setAdminMailboxAutoDeleteEnabled(event.target.checked)}
                              />
                              <span>Automatically delete old messages</span>
                            </label>
                            {adminMailboxAutoDeleteEnabled ? (
                              <label className="settingsInlineField">
                                <span>Delete after</span>
                                <input
                                  type="number"
                                  min={1}
                                  step={1}
                                  value={adminMailboxDraft.autoDeleteAfterDays > 0 ? adminMailboxDraft.autoDeleteAfterDays : defaultAutoDeleteDays}
                                  onChange={(event) =>
                                    setAdminMailboxDraft((current) => ({
                                      ...current,
                                      autoDeleteAfterDays: Math.max(
                                        1,
                                        Number.parseInt(event.target.value || String(defaultAutoDeleteDays), 10)
                                      )
                                    }))
                                  }
                                />
                                <span>days</span>
                              </label>
                            ) : null}
                          </div>
                        </div>
                      ) : null}
                    </section>

                    <div className="adminUserLayout">
                      <section className="settingsField adminUsersCard">
                        <div className="adminSectionHeader">
                          <span>Users</span>
                          <button className="ghostButton compactButton" onClick={handleCreateManagedUser}>
                            Create user
                          </button>
                        </div>
                        <div className="adminUserList">
                          {managedUsers.length > 0 ? (
                            managedUsers.map((user) => (
                              <button
                                key={user.id}
                                className={user.id === selectedManagedUserId ? "messageItem active adminUserItem" : "messageItem adminUserItem"}
                                onClick={() => {
                                  setSelectedManagedUserId(user.id);
                                  applyManagedUserDraft(user);
                                }}
                              >
                                <strong>{user.username}</strong>
                                <span className="mutedText">{user.settings.acceptedRcptDomains || "No recipient filter"}</span>
                              </button>
                            ))
                          ) : (
                            <div className="emptyListCard">
                              <strong>No users yet</strong>
                              <p>Create the first local user on the right.</p>
                            </div>
                          )}
                        </div>
                      </section>

                      <section className="settingsField adminEditorCard">
                        <div className="adminSectionHeader">
                          <div>
                            <span>{selectedManagedUserId ? "Edit user" : "User policies"}</span>
                            <p className="adminEditorLead">
                              {selectedManagedUserId ? "Update login and delivery policy for this user." : "Select a user on the left or create a new one first."}
                            </p>
                          </div>
                          {selectedManagedUserId ? (
                            <button className="dangerButton compactButton" disabled={settingsSaving} onClick={() => void handleSaveManagedUser()}>
                              {settingsSaving ? "Saving..." : "Save user"}
                            </button>
                          ) : null}
                        </div>

                        {selectedManagedUserId ? (
                        <div className="settingsGrid adminEditorGrid">
                          <label className="settingsField">
                            <span>Username</span>
                            <input value={managedUsername} onChange={(event) => setManagedUsername(event.target.value)} />
                            <small>Local username used for login.</small>
                          </label>
                          <label className="settingsField">
                            <span>{selectedManagedUserId ? "New password" : "Password"}</span>
                            <input
                              type="password"
                              value={managedPassword}
                              onChange={(event) => setManagedPassword(event.target.value)}
                              placeholder={selectedManagedUserId ? "Leave empty to keep the current password" : ""}
                            />
                            <small>{selectedManagedUserId ? "Optional on update." : "Required when creating a user."}</small>
                          </label>

                          <div className="settingsField toggleField">
                            <span>MailFail enabled</span>
                            <label className="toggleRow">
                              <input
                                type="checkbox"
                                checked={settingsDraft.mailFailEnabled}
                                onChange={(event) => handleToggleMailFail(event.target.checked)}
                              />
                              <span>Enable MailFail rule evaluation</span>
                            </label>
                            <small>Turns MailFail rule evaluation on for incoming SMTP sessions.</small>
                          </div>

                          {settingsDraft.mailFailEnabled ? (
                            <label className="settingsField">
                              <span>MailFail rules file</span>
                              <input
                                value={settingsDraft.mailFailRulesFile}
                                onChange={(event) => updateSettingsField("mailFailRulesFile", event.target.value)}
                                placeholder={defaultMailFailRulesFile}
                              />
                              <small>Defaults to {defaultMailFailRulesFile} unless you override it.</small>
                            </label>
                          ) : null}

                          <div className="settingsField toggleField">
                            <span>Allowed remote IPs</span>
                            <label className="toggleRow">
                              <input type="checkbox" checked={limitAllowedRemoteIps} onChange={(event) => setLimitAllowedRemoteIps(event.target.checked)} />
                              <span>Restrict SMTP connections to specific IPs or CIDRs</span>
                            </label>
                            {limitAllowedRemoteIps ? (
                              <textarea
                                rows={3}
                                value={settingsDraft.allowedRemoteIps}
                                onChange={(event) => updateSettingsField("allowedRemoteIps", event.target.value)}
                              />
                            ) : null}
                            <small>Comma-separated IPs or CIDR ranges allowed to connect via SMTP.</small>
                          </div>

                          <div className="settingsField toggleField">
                            <span>Accepted recipient domains</span>
                            <label className="toggleRow">
                              <input type="checkbox" checked={limitAcceptedRcptDomains} onChange={(event) => setLimitAcceptedRcptDomains(event.target.checked)} />
                              <span>Restrict accepted recipient domains</span>
                            </label>
                            {limitAcceptedRcptDomains ? (
                              <textarea
                                rows={3}
                                value={settingsDraft.acceptedRcptDomains}
                                onChange={(event) => updateSettingsField("acceptedRcptDomains", event.target.value)}
                              />
                            ) : null}
                            <small>Comma-separated recipient domains or regex patterns to accept.</small>
                          </div>

                          <div className="settingsField toggleField">
                            <span>Accepted sender domains</span>
                            <label className="toggleRow">
                              <input type="checkbox" checked={limitAcceptedFromDomains} onChange={(event) => setLimitAcceptedFromDomains(event.target.checked)} />
                              <span>Restrict accepted sender domains</span>
                            </label>
                            {limitAcceptedFromDomains ? (
                              <textarea
                                rows={3}
                                value={settingsDraft.acceptedFromDomains}
                                onChange={(event) => updateSettingsField("acceptedFromDomains", event.target.value)}
                              />
                            ) : null}
                            <small>Comma-separated sender domains or regex patterns to accept.</small>
                          </div>

                          <div className="settingsField toggleField">
                            <span>Automatic message deletion</span>
                            <label className="toggleRow">
                              <input type="checkbox" checked={autoDeleteEnabled} onChange={(event) => handleToggleAutoDelete(event.target.checked)} />
                              <span>Automatically delete old messages</span>
                            </label>
                            {autoDeleteEnabled ? (
                              <label className="settingsInlineField">
                                <span>Delete after</span>
                                <input
                                  type="number"
                                  min={1}
                                  step={1}
                                  value={settingsDraft.autoDeleteAfterDays > 0 ? settingsDraft.autoDeleteAfterDays : defaultAutoDeleteDays}
                                  onChange={(event) =>
                                    updateSettingsField(
                                      "autoDeleteAfterDays",
                                      Math.max(1, Number.parseInt(event.target.value || String(defaultAutoDeleteDays), 10))
                                    )
                                  }
                                />
                                <span>days</span>
                              </label>
                            ) : null}
                            <small>Deletes messages after the configured number of days.</small>
                          </div>
                        </div>
                        ) : (
                          <div className="emptyListCard adminEditorEmpty">
                            <strong>No user selected</strong>
                            <p>Create a user from the left column, then edit delivery policies here.</p>
                          </div>
                        )}
                      </section>
                    </div>
                  </div>
                ) : (
                  <div className="settingsGrid">
                  <div className="settingsField toggleField">
                    <span>MailFail enabled</span>
                    <label className="toggleRow">
                      <input
                        type="checkbox"
                        checked={settingsDraft.mailFailEnabled}
                        onChange={(event) => handleToggleMailFail(event.target.checked)}
                      />
                      <span>Enable MailFail rule evaluation</span>
                    </label>
                    <small>Turns MailFail rule evaluation on for incoming SMTP sessions.</small>
                  </div>

                  {settingsDraft.mailFailEnabled ? (
                    <label className="settingsField">
                      <span>MailFail rules file</span>
                      <input
                        value={settingsDraft.mailFailRulesFile}
                        onChange={(event) => updateSettingsField("mailFailRulesFile", event.target.value)}
                        placeholder={defaultMailFailRulesFile}
                      />
                      <small>Defaults to {defaultMailFailRulesFile} unless you override it.</small>
                    </label>
                  ) : null}

                  <div className="settingsField toggleField">
                    <span>Allowed remote IPs</span>
                    <label className="toggleRow">
                      <input type="checkbox" checked={limitAllowedRemoteIps} onChange={(event) => setLimitAllowedRemoteIps(event.target.checked)} />
                      <span>Restrict SMTP connections to specific IPs or CIDRs</span>
                    </label>
                    {limitAllowedRemoteIps ? (
                      <textarea
                        rows={3}
                        value={settingsDraft.allowedRemoteIps}
                        onChange={(event) => updateSettingsField("allowedRemoteIps", event.target.value)}
                      />
                    ) : null}
                    <small>Comma-separated IPs or CIDR ranges allowed to connect via SMTP.</small>
                  </div>

                  <div className="settingsField toggleField">
                    <span>Accepted recipient domains</span>
                    <label className="toggleRow">
                      <input type="checkbox" checked={limitAcceptedRcptDomains} onChange={(event) => setLimitAcceptedRcptDomains(event.target.checked)} />
                      <span>Restrict accepted recipient domains</span>
                    </label>
                    {limitAcceptedRcptDomains ? (
                      <textarea
                        rows={3}
                        value={settingsDraft.acceptedRcptDomains}
                        onChange={(event) => updateSettingsField("acceptedRcptDomains", event.target.value)}
                      />
                    ) : null}
                    <small>Comma-separated recipient domains or regex patterns to accept.</small>
                  </div>

                  <div className="settingsField toggleField">
                    <span>Accepted sender domains</span>
                    <label className="toggleRow">
                      <input type="checkbox" checked={limitAcceptedFromDomains} onChange={(event) => setLimitAcceptedFromDomains(event.target.checked)} />
                      <span>Restrict accepted sender domains</span>
                    </label>
                    {limitAcceptedFromDomains ? (
                      <textarea
                        rows={3}
                        value={settingsDraft.acceptedFromDomains}
                        onChange={(event) => updateSettingsField("acceptedFromDomains", event.target.value)}
                      />
                    ) : null}
                    <small>Comma-separated sender domains or regex patterns to accept.</small>
                  </div>

                  <div className="settingsField toggleField">
                    <span>Automatic message deletion</span>
                    <label className="toggleRow">
                      <input type="checkbox" checked={autoDeleteEnabled} onChange={(event) => handleToggleAutoDelete(event.target.checked)} />
                      <span>Automatically delete old messages</span>
                    </label>
                    {autoDeleteEnabled ? (
                      <label className="settingsInlineField">
                        <span>Delete after</span>
                        <input
                          type="number"
                          min={1}
                          step={1}
                          value={settingsDraft.autoDeleteAfterDays > 0 ? settingsDraft.autoDeleteAfterDays : defaultAutoDeleteDays}
                          onChange={(event) =>
                            updateSettingsField(
                              "autoDeleteAfterDays",
                              Math.max(1, Number.parseInt(event.target.value || String(defaultAutoDeleteDays), 10))
                            )
                          }
                        />
                        <span>days</span>
                      </label>
                    ) : null}
                    <small>Deletes messages after the configured number of days.</small>
                  </div>
                  </div>
                )
              ) : null}

              <div className="settingsActions">
                {session?.isAdmin && selectedManagedUserId ? (
                  <button className="ghostButton compactButton" disabled={settingsSaving} onClick={() => void handleDeleteManagedUser()}>
                    Delete user
                  </button>
                ) : null}
                <button className="ghostButton compactButton" onClick={() => setSettingsOpen(false)}>
                  Cancel
                </button>
                {!session?.isAdmin ? (
                  <button className="dangerButton compactButton" disabled={settingsLoading || settingsSaving} onClick={() => void handleSaveSettings()}>
                    {settingsSaving ? "Saving..." : "Save settings"}
                  </button>
                ) : null}
              </div>
            </section>
          </div>
        ) : null}

        {settingsOpen && session?.isAdmin && createUserOpen ? (
          <div className="settingsOverlay nestedOverlay" onClick={() => setCreateUserOpen(false)}>
            <section className="createUserDialog" onClick={(event) => event.stopPropagation()}>
              <div className="settingsPanelHeader">
                <div>
                  <p className="eyebrow">Admin area</p>
                  <h2>Create user</h2>
                </div>
                <button className="ghostButton compactButton" onClick={() => setCreateUserOpen(false)}>
                  Close
                </button>
              </div>

              <p className="settingsLead">Create a local user first. Delivery policies can be edited right after creation.</p>

              <div className="settingsGrid createUserGrid">
                <label className="settingsField">
                  <span>Username</span>
                  <input value={managedUsername} onChange={(event) => setManagedUsername(event.target.value)} />
                  <small>Local username used for login.</small>
                </label>
                <label className="settingsField">
                  <span>Password</span>
                  <input type="password" value={managedPassword} onChange={(event) => setManagedPassword(event.target.value)} />
                  <small>Required when creating a user.</small>
                </label>
              </div>

              <div className="settingsActions">
                <button className="ghostButton compactButton" onClick={() => setCreateUserOpen(false)}>
                  Cancel
                </button>
                <button className="dangerButton compactButton" disabled={settingsSaving} onClick={() => void handleCreateUserWithCredentials()}>
                  {settingsSaving ? "Creating..." : "Create user"}
                </button>
              </div>
            </section>
          </div>
        ) : null}
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

function normalizeSettingsDraft(settings: AppSettings): AppSettings {
  return {
    ...settings,
    mailFailRulesFile: settings.mailFailRulesFile.trim() || defaultMailFailRulesFile,
    autoDeleteAfterDays: settings.autoDeleteAfterDays > 0 ? settings.autoDeleteAfterDays : 0
  };
}

function buildSettingsPayload(
  settings: AppSettings,
  toggles: {
    limitAllowedRemoteIps: boolean;
    limitAcceptedRcptDomains: boolean;
    limitAcceptedFromDomains: boolean;
    autoDeleteEnabled: boolean;
  }
): AppSettings {
  return {
    ...settings,
    allowedOrigins: "",
    smtpLogVerbose: false,
    mailFailRulesFile: settings.mailFailEnabled ? settings.mailFailRulesFile.trim() || defaultMailFailRulesFile : defaultMailFailRulesFile,
    allowedRemoteIps: toggles.limitAllowedRemoteIps ? settings.allowedRemoteIps : "",
    acceptedRcptDomains: toggles.limitAcceptedRcptDomains ? settings.acceptedRcptDomains : "",
    acceptedFromDomains: toggles.limitAcceptedFromDomains ? settings.acceptedFromDomains : "",
    autoDeleteAfterDays: toggles.autoDeleteEnabled ? Math.max(1, settings.autoDeleteAfterDays || defaultAutoDeleteDays) : 0
  };
}

function buildEMLFileName(message: Message): string {
  const subject = sanitizeFileName(message.subject || "message");
  const stamp = new Date(message.receivedAt).toISOString().replace(/:/g, "-");
  return `${subject}-${stamp}.eml`;
}

function prepareHtmlPreview(html: string): string {
  const previewHead = [
    '<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">',
    "<style>",
    "html, body { margin: 0; padding: 0; max-width: 100%; overflow-x: auto; -webkit-text-size-adjust: 100%; }",
    "img, video, iframe { max-width: 100% !important; height: auto !important; }",
    "table { max-width: 100% !important; }",
    "</style>"
  ].join("");

  if (/<head[\s>]/i.test(html)) {
    return html.replace(/<head([^>]*)>/i, `<head$1>${previewHead}`);
  }

  if (/<html[\s>]/i.test(html)) {
    return html.replace(/<html([^>]*)>/i, `<html$1><head>${previewHead}</head>`);
  }

  return `<!doctype html><html><head>${previewHead}</head><body>${html}</body></html>`;
}

function sanitizeFileName(value: string): string {
  const normalized = value
    .trim()
    .replace(/[<>:"/\\|?*\u0000-\u001F]/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");

  if (!normalized) {
    return "message";
  }

  return normalized.slice(0, 80);
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
