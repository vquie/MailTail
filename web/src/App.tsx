import { useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
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
import type { AppSettings, MailFailRule, Message, SessionInfo, Stats, User } from "./types";

type TabKey = "html" | "text" | "headers" | "raw" | "attachments";
type MailFailSettingsScope = "user" | "adminMailbox";
type SettingsTab = "instance" | "adminMailbox" | "users" | "delivery" | "retention";
type SettingsSubTab = "general" | "rules";

const defaultPageSize = 25;
const defaultAutoDeleteDays = 30;
const defaultMailFailRulesTemplate: MailFailRule[] = [
  {
    name: "user-unknown",
    trigger: "mf-user-unknown",
    stage: "rcpt",
    action: "reject",
    allowAfter: 1,
    minRetryAfter: "",
    resetAfter: "",
    code: 550,
    enhancedCode: "5.1.1",
    feedbackType: "",
    reportRecipientLocalPart: "",
    message: "User unknown"
  },
  {
    name: "greylist",
    trigger: "mf-greylist",
    stage: "rcpt",
    action: "greylist",
    allowAfter: 1,
    minRetryAfter: "5m",
    resetAfter: "1h",
    code: 451,
    enhancedCode: "4.7.1",
    feedbackType: "",
    reportRecipientLocalPart: "",
    message: "Try again later"
  },
  {
    name: "quota",
    trigger: "mf-quota",
    stage: "data",
    action: "reject",
    allowAfter: 1,
    minRetryAfter: "",
    resetAfter: "",
    code: 552,
    enhancedCode: "5.2.2",
    feedbackType: "",
    reportRecipientLocalPart: "",
    message: "Mailbox full"
  }
];
const emptyMailFailRule: MailFailRule = {
  name: "",
  trigger: "",
  stage: "rcpt",
  action: "reject",
  allowAfter: 1,
  minRetryAfter: "",
  resetAfter: "1h",
  code: 550,
  enhancedCode: "",
  feedbackType: "",
  reportRecipientLocalPart: "",
  message: ""
};
const emptySettings: AppSettings = {
  allowedOrigins: "",
  smtpLogVerbose: false,
  mailFailEnabled: false,
  mailFailRules: [],
  reportFrom: "",
  allowedRemoteIps: "",
  acceptedRcptDomains: "",
  acceptedFromDomains: "",
  autoDeleteAfterDays: 0
};

const emptyManagedUser = {
  username: "",
  password: ""
};

type FlashTone = "error" | "success";

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
  const [selectedTag, setSelectedTag] = useState("");
  const [availableTags, setAvailableTags] = useState<string[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [activeTab, setActiveTab] = useState<TabKey>("html");
  const [autoRefreshEnabled, setAutoRefreshEnabled] = useState(true);
  const [sidebarDiagnosticsOpen, setSidebarDiagnosticsOpen] = useState(false);
  const [messageDetailsOpen, setMessageDetailsOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [lastSyncAt, setLastSyncAt] = useState<string | null>(null);
  const [copiedHeaderKey, setCopiedHeaderKey] = useState<string | null>(null);
  const [copiedPaneKey, setCopiedPaneKey] = useState<TabKey | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [aboutOpen, setAboutOpen] = useState(false);
  const [activeSettingsTab, setActiveSettingsTab] = useState<SettingsTab>("instance");
  const [adminMailboxSubTab, setAdminMailboxSubTab] = useState<SettingsSubTab>("general");
  const [userEditorSubTab, setUserEditorSubTab] = useState<SettingsSubTab>("general");
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
  const [mailFailManagerScope, setMailFailManagerScope] = useState<MailFailSettingsScope | null>(null);
  const [selectedMailFailRuleIndex, setSelectedMailFailRuleIndex] = useState<number | null>(null);
  const [mailFailRuleDraft, setMailFailRuleDraft] = useState<MailFailRule>(emptyMailFailRule);
  const [managedUsername, setManagedUsername] = useState(emptyManagedUser.username);
  const [managedPassword, setManagedPassword] = useState(emptyManagedUser.password);
  const [error, setError] = useState<string | null>(null);
  const selectedManagedUser = useMemo(
    () => managedUsers.find((user) => user.id === selectedManagedUserId) ?? null,
    [managedUsers, selectedManagedUserId]
  );
  const queryRef = useRef(query);
  const selectedTagRef = useRef(selectedTag);
  const messagesRef = useRef(messages);
  const selectedIdRef = useRef<number | null>(selectedId);
  const selectedMessageRef = useRef<Message | null>(selectedMessage);
  const userMenuRef = useRef<HTMLDetailsElement | null>(null);
  const nextCursorRef = useRef(nextCursor);
  const hasMoreRef = useRef(hasMore);

  useEffect(() => {
    queryRef.current = query;
  }, [query]);

  useEffect(() => {
    selectedTagRef.current = selectedTag;
  }, [selectedTag]);

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
    if (!settingsNotice) {
      return;
    }

    const timer = window.setTimeout(() => {
      setSettingsNotice(null);
    }, 4000);

    return () => window.clearTimeout(timer);
  }, [settingsNotice]);

  useEffect(() => {
    void loadSessionInfo();
  }, []);

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

      if (!autoRefreshEnabled) {
        return;
      }

      void loadOverview(queryRef.current, {
        preferredId: selectedIdRef.current,
        setBusy: false
      });
    }, 15000);

    return () => window.clearInterval(timer);
  }, [autoRefreshEnabled, query, selectedTag]);

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
        fetchMessages(search, selectedTagRef.current, "", defaultPageSize),
        fetchStats(),
        fetchAppInfo()
      ]);
      const loadedMore = messagesRef.current.length > page.messages.length;
      const messageList = resetList ? page.messages : mergeMessages(page.messages, messagesRef.current);
      setMessages(messageList);
      setAvailableTags(page.availableTags ?? []);
      if (selectedTagRef.current && !(page.availableTags ?? []).includes(selectedTagRef.current)) {
        setSelectedTag("");
      }
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
      setLastSyncAt(new Date().toISOString());
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
      const page = await fetchMessages(queryRef.current, selectedTagRef.current, nextCursorRef.current, defaultPageSize);
      setMessages((current) => mergeMessages(current, page.messages));
      setAvailableTags(page.availableTags ?? []);
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
    const activeQuery = query.trim();
    const activeTag = selectedTag.trim();
    const confirmation = describeDeleteScope(activeQuery, activeTag);
    if (!window.confirm(confirmation)) {
      return;
    }

    await clearInbox(activeQuery, activeTag);
    await loadOverview(activeQuery, { preferredId: null, forceDetail: true, resetList: true });
  }

  async function handleLogout() {
    await logout();
    window.location.href = "/login";
  }

  async function openSettingsPanel() {
    try {
      userMenuRef.current?.removeAttribute("open");
      setSettingsOpen(true);
      setSettingsLoading(true);
      setActiveSettingsTab(session?.isAdmin ? "instance" : "delivery");
      clearSettingsFlash();
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

  function openAboutPanel() {
    userMenuRef.current?.removeAttribute("open");
    setAboutOpen(true);
  }

  async function handleSaveSettings(settingsToSave: AppSettings = settingsDraft) {
    if (session?.isAdmin) {
      await handleSaveManagedUser();
      return;
    }

    try {
      setSettingsSaving(true);
      clearSettingsFlash();
      const saved = await updateSettings(buildSettingsPayload(settingsToSave, {
        limitAllowedRemoteIps,
        limitAcceptedRcptDomains,
        limitAcceptedFromDomains,
        autoDeleteEnabled
      }));
      const normalized = normalizeSettingsDraft(saved);
      setSettingsDraft(normalized);
      setCurrentSettings(normalized);
      setSettingsLoaded(true);
      showSettingsNotice("Saved. Changes are applied immediately.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to save settings");
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

  async function handleSaveManagedUser(settingsToSave: AppSettings = settingsDraft) {
    try {
      setSettingsSaving(true);
      clearSettingsFlash();
      const payload = {
        username: managedUsername.trim(),
        password: managedPassword,
        settings: buildSettingsPayload(settingsToSave, {
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
      showSettingsNotice(selectedManagedUserId ? "User updated." : "User created.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to save user");
    } finally {
      setSettingsSaving(false);
    }
  }

  async function handleSaveGlobalSettings() {
    try {
      setSettingsSaving(true);
      clearSettingsFlash();
      const saved = await updateSettings({
        ...emptySettings,
        allowedOrigins: globalSettingsDraft.allowedOrigins,
        smtpLogVerbose: globalSettingsDraft.smtpLogVerbose
      });
      setGlobalSettingsDraft(normalizeSettingsDraft(saved));
      showSettingsNotice("Platform settings saved.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to save platform settings");
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

  async function handleSaveAdminMailboxSettings(settingsToSave: AppSettings = adminMailboxDraft) {
    try {
      setSettingsSaving(true);
      clearSettingsFlash();
      const saved = await updateAdminMailboxSettings(
        adminMailboxEnabled
          ? buildSettingsPayload(settingsToSave, {
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
      showSettingsNotice("Admin mailbox settings saved.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to save admin mailbox settings");
    } finally {
      setSettingsSaving(false);
    }
  }

  async function handleCreateUserWithCredentials() {
    try {
      setSettingsSaving(true);
      clearSettingsFlash();
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
      showSettingsNotice("User created. Configure delivery policies on the right.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to create user");
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
      clearSettingsFlash();
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
      showSettingsNotice("User deleted.");
    } catch (err) {
      showSettingsError(err instanceof Error ? err.message : "Failed to delete user");
    } finally {
      setSettingsSaving(false);
    }
  }

  function clearSettingsFlash() {
    setSettingsError(null);
    setSettingsNotice(null);
  }

  function showSettingsNotice(message: string) {
    setSettingsError(null);
    setSettingsNotice(message);
  }

  function showSettingsError(message: string) {
    setSettingsNotice(null);
    setSettingsError(message);
  }

  function handleCreateManagedUser() {
    setCreateUserOpen(true);
    setActiveSettingsTab("users");
    setUserEditorSubTab("general");
    setSelectedManagedUserId(null);
    applyManagedUserDraft(null);
    clearSettingsFlash();
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
    clearSettingsFlash();
  }

  function handleToggleMailFail(enabled: boolean) {
    updateSettingsField("mailFailEnabled", enabled);
    if (enabled && settingsDraft.mailFailRules.length === 0) {
      updateSettingsField("mailFailRules", cloneMailFailRules(defaultMailFailRulesTemplate));
    }
  }

  function handleRulesActivation(
    scope: MailFailSettingsScope,
    enabled: boolean,
    onToggle: (enabled: boolean) => void
  ) {
    onToggle(enabled);
    setMailFailManagerScope(scope);

    if (!enabled) {
      setSelectedMailFailRuleIndex(null);
      setMailFailRuleDraft(createDefaultMailFailRule());
      return;
    }

    const firstRule = currentScopedSettings(scope).mailFailRules?.[0] ?? defaultMailFailRulesTemplate[0];
    setSelectedMailFailRuleIndex(0);
    setMailFailRuleDraft(cloneMailFailRule(firstRule));
  }

  function updateScopedSettings(scope: MailFailSettingsScope, updater: (current: AppSettings) => AppSettings) {
    if (scope === "adminMailbox") {
      setAdminMailboxDraft((current) => updater(current));
    } else {
      setSettingsDraft((current) => updater(current));
    }
    clearSettingsFlash();
  }

  function currentScopedSettings(scope: MailFailSettingsScope): AppSettings {
    return scope === "adminMailbox" ? adminMailboxDraft : settingsDraft;
  }

  function activateMailFailWorkspace(
    scope: MailFailSettingsScope,
    options: {
      selectedIndex?: number | null;
      createNew?: boolean;
    } = {}
  ) {
    const settings = currentScopedSettings(scope);
    const rules = settings.mailFailRules ?? [];
    setMailFailManagerScope(scope);
    if (options.createNew) {
      setSelectedMailFailRuleIndex(null);
      setMailFailRuleDraft(createDefaultMailFailRule());
      return;
    }

    const desiredIndex = options.selectedIndex ?? 0;
    const rule = desiredIndex !== null ? rules[desiredIndex] : null;
    if (rule) {
      setSelectedMailFailRuleIndex(desiredIndex);
      setMailFailRuleDraft(cloneMailFailRule(rule));
    } else {
      setSelectedMailFailRuleIndex(null);
      setMailFailRuleDraft(createDefaultMailFailRule());
    }
  }

  function handleStartNewMailFailRule(scope: MailFailSettingsScope) {
    if (mailFailManagerScope === scope && stageMailFailRuleForSave(scope) === null) {
      return;
    }
    setMailFailManagerScope(scope);
    setSelectedMailFailRuleIndex(null);
    setMailFailRuleDraft(createDefaultMailFailRule());
  }

  function handleSelectMailFailRule(scope: MailFailSettingsScope, index: number) {
    if (mailFailManagerScope === scope && selectedMailFailRuleIndex === index) {
      return;
    }
    const stagedSettings = mailFailManagerScope === scope ? stageMailFailRuleForSave(scope) : currentScopedSettings(scope);
    if (!stagedSettings) {
      return;
    }
    const rules = stagedSettings.mailFailRules ?? [];
    const rule = rules[index];
    if (!rule) {
      return;
    }
    setMailFailManagerScope(scope);
    setSelectedMailFailRuleIndex(index);
    setMailFailRuleDraft(cloneMailFailRule(rule));
  }

  function handleImportMailFailTemplate(scope: MailFailSettingsScope) {
    const existingRules = currentScopedSettings(scope).mailFailRules ?? [];
    if (existingRules.length > 0 && !window.confirm("Replace the current rule catalog with the example rules?")) {
      return;
    }
    updateScopedSettings(scope, (current) => ({
      ...current,
      mailFailEnabled: true,
      mailFailRules: cloneMailFailRules(defaultMailFailRulesTemplate)
    }));
    setMailFailManagerScope(scope);
    setSelectedMailFailRuleIndex(0);
    setMailFailRuleDraft(cloneMailFailRule(defaultMailFailRulesTemplate[0]));
  }

  function handleDeleteMailFailRule(scope: MailFailSettingsScope, index: number) {
    const rules = currentScopedSettings(scope).mailFailRules ?? [];
    const nextRules = rules.filter((_, ruleIndex) => ruleIndex !== index);
    updateScopedSettings(scope, (current) => ({
      ...current,
      mailFailRules: nextRules
    }));
    setMailFailManagerScope(scope);

    if (nextRules.length === 0) {
      setSelectedMailFailRuleIndex(null);
      setMailFailRuleDraft(createDefaultMailFailRule());
      return;
    }

    const nextIndex = Math.min(index, nextRules.length - 1);
    setSelectedMailFailRuleIndex(nextIndex);
    setMailFailRuleDraft(cloneMailFailRule(nextRules[nextIndex]));
  }

  function updateMailFailRuleEditor(scope: MailFailSettingsScope, update: (rule: MailFailRule) => MailFailRule) {
    const scopedSettings = currentScopedSettings(scope);
    const selectedRule =
      selectedMailFailRuleIndex !== null ? scopedSettings.mailFailRules?.[selectedMailFailRuleIndex] : null;
    const nextRule = update(cloneMailFailRule(selectedRule ?? mailFailRuleDraft));
    setMailFailRuleDraft(nextRule);

    if (selectedMailFailRuleIndex !== null && selectedRule) {
      updateScopedSettings(scope, (current) => {
        const nextRules = [...(current.mailFailRules ?? [])];
        nextRules[selectedMailFailRuleIndex] = nextRule;
        return { ...current, mailFailRules: nextRules };
      });
    }
  }

  function stageMailFailRuleForSave(scope: MailFailSettingsScope): AppSettings | null {
    const scopedSettings = currentScopedSettings(scope);
    if (mailFailManagerScope !== scope) {
      return scopedSettings;
    }

    const hasPendingRule = selectedMailFailRuleIndex !== null || hasMailFailRuleDraftChanges(mailFailRuleDraft);
    if (!hasPendingRule) {
      return scopedSettings;
    }

    const sourceRule =
      selectedMailFailRuleIndex !== null
        ? scopedSettings.mailFailRules?.[selectedMailFailRuleIndex] ?? mailFailRuleDraft
        : mailFailRuleDraft;
    const normalizedRule = normalizeMailFailRuleDraft(sourceRule);
    if (!normalizedRule.trigger) {
      showSettingsError("MailFail rules require a trigger.");
      return null;
    }
    if (!isReportAction(normalizedRule.action) && !normalizedRule.message) {
      showSettingsError("MailFail rules require a reply message.");
      return null;
    }
    if (
      supportsReportRecipientLocalPart(normalizedRule.action) &&
      normalizedRule.reportRecipientLocalPart &&
      !isValidReportRecipientLocalPart(normalizedRule.reportRecipientLocalPart)
    ) {
      showSettingsError("Report recipient must be a local part only, without @ or a domain.");
      return null;
    }
    const rules = scopedSettings.mailFailRules ?? [];
    const nextRules = [...rules];

    if (selectedMailFailRuleIndex === null) {
      nextRules.push(normalizedRule);
      setSelectedMailFailRuleIndex(nextRules.length - 1);
    } else {
      nextRules[selectedMailFailRuleIndex] = normalizedRule;
    }

    const nextSettings = {
      ...scopedSettings,
      mailFailEnabled: true,
      mailFailRules: nextRules
    };
    updateScopedSettings(scope, () => nextSettings);
    setMailFailManagerScope(scope);
    setMailFailRuleDraft(normalizedRule);
    return nextSettings;
  }

  async function handleSaveActiveSettings() {
    if (session?.isAdmin) {
      if (activeSettingsTab === "instance") {
        await handleSaveGlobalSettings();
        return;
      }
      if (activeSettingsTab === "adminMailbox") {
        const settingsToSave =
          adminMailboxSubTab === "rules" ? stageMailFailRuleForSave("adminMailbox") : adminMailboxDraft;
        if (settingsToSave) {
          await handleSaveAdminMailboxSettings(settingsToSave);
        }
        return;
      }
      if (activeSettingsTab === "users" && selectedManagedUserId) {
        const settingsToSave = userEditorSubTab === "rules" ? stageMailFailRuleForSave("user") : settingsDraft;
        if (settingsToSave) {
          await handleSaveManagedUser(settingsToSave);
        }
      }
      return;
    }

    await handleSaveSettings();
  }

  function handleSettingsSubTabChange(scope: MailFailSettingsScope, nextTab: SettingsSubTab) {
    const currentTab = scope === "adminMailbox" ? adminMailboxSubTab : userEditorSubTab;
    if (currentTab === "rules" && nextTab !== "rules" && stageMailFailRuleForSave(scope) === null) {
      return;
    }
    if (scope === "adminMailbox") {
      setAdminMailboxSubTab(nextTab);
    } else {
      setUserEditorSubTab(nextTab);
    }
  }

  function handleSettingsSectionChange(nextTab: SettingsTab) {
    if (nextTab === activeSettingsTab) {
      return;
    }
    if (
      activeSettingsTab === "adminMailbox" &&
      adminMailboxSubTab === "rules" &&
      stageMailFailRuleForSave("adminMailbox") === null
    ) {
      return;
    }
    if (activeSettingsTab === "users" && userEditorSubTab === "rules" && stageMailFailRuleForSave("user") === null) {
      return;
    }
    setActiveSettingsTab(nextTab);
  }

  function handleToggleAutoDelete(enabled: boolean) {
    setAutoDeleteEnabled(enabled);
    if (enabled && settingsDraft.autoDeleteAfterDays <= 0) {
      updateSettingsField("autoDeleteAfterDays", defaultAutoDeleteDays);
    }
  }

  function renderRulesWorkspace(options: {
    scope: MailFailSettingsScope;
    enabled: boolean;
    onToggle: (enabled: boolean) => void;
    exampleRecipient: string;
  }) {
    const scopedSettings = currentScopedSettings(options.scope);
    const rules = scopedSettings.mailFailRules ?? [];
    const isActiveScope = mailFailManagerScope === options.scope;
    const resolvedRuleIndex = isActiveScope
      ? selectedMailFailRuleIndex !== null && rules[selectedMailFailRuleIndex]
        ? selectedMailFailRuleIndex
        : null
      : rules.length > 0
        ? 0
        : null;
    const editorDraft = isActiveScope
      ? selectedMailFailRuleIndex !== null && rules[selectedMailFailRuleIndex]
        ? cloneMailFailRule(rules[selectedMailFailRuleIndex])
        : mailFailRuleDraft
      : resolvedRuleIndex !== null
        ? cloneMailFailRule(rules[resolvedRuleIndex])
        : createDefaultMailFailRule();

    return (
      <div className="rulesWorkspace rulesWorkspaceFocused">
        <section className="rulesControlBar">
          <label className="rulesEnableControl">
            <input
              type="checkbox"
              checked={options.enabled}
              onChange={(event) => handleRulesActivation(options.scope, event.target.checked, options.onToggle)}
            />
            <span>
              <strong>MailFail rules</strong>
              <small>{options.enabled ? `${rules.length} configured · evaluated on inbound SMTP` : "Disabled for this mailbox"}</small>
            </span>
          </label>
          {options.enabled ? (
            <label className="rulesSenderControl">
              <span>Report sender</span>
              <input
                type="email"
                value={scopedSettings.reportFrom}
                onChange={(event) =>
                  updateScopedSettings(options.scope, (current) => ({ ...current, reportFrom: event.target.value }))
                }
                placeholder={`postmaster@${mailFailExampleDomain}`}
              />
              <small>Leave empty to derive <code>postmaster@domain</code> from an exact recipient domain.</small>
            </label>
          ) : null}
        </section>

        {options.enabled ? (
          <div className="mailFailWorkspaceLayout">
              <section className="settingsField settingsCard rulesCatalogCard">
                <div className="settingsCardHeader">
                  <div>
                    <h3>Rules</h3>
                    <p className="settingsCardLead">Choose a rule to edit it, or add a new one.</p>
                  </div>
                  <div className="settingsCardActions">
                    {resolvedRuleIndex !== null ? (
                      <button
                        className="ghostButton compactButton"
                        type="button"
                        onClick={() => handleStartNewMailFailRule(options.scope)}
                      >
                        Add rule
                      </button>
                    ) : null}
                    <button className="ghostButton compactButton" type="button" onClick={() => handleImportMailFailTemplate(options.scope)}>
                      Load example rules
                    </button>
                  </div>
                </div>

                {rules.length ? (
                  <div className="rulesCatalogList">
                    {rules.map((rule, index) => (
                      <button
                        key={`${rule.name}-${rule.trigger}-${index}`}
                        className={index === resolvedRuleIndex ? "rulesCatalogItem active" : "rulesCatalogItem"}
                        type="button"
                        onClick={() => handleSelectMailFailRule(options.scope, index)}
                      >
                        <div className="rulesCatalogItemTop">
                          <strong>{rule.name || rule.trigger}</strong>
                          <span className="ruleChip">{isReportAction(rule.action) ? "report" : rule.code}</span>
                        </div>
                        <div className="rulesCatalogMeta">
                          <span>{rule.trigger}</span>
                          {!isReportAction(rule.action) ? <span>{rule.stage.toUpperCase()}</span> : null}
                          <span>{rule.action}</span>
                        </div>
                        <p>{mailFailRuleSummary(rule)}</p>
                      </button>
                    ))}
                  </div>
                ) : (
                  <div className="emptyListCard">
                    <strong>No rules yet</strong>
                    <p>Start with an example set or create the first rule for this mailbox.</p>
                  </div>
                )}
              </section>

              <section className="settingsField settingsCard mailFailEditorCard">
                <div className="settingsCardHeader">
                  <div>
                    <h3>{resolvedRuleIndex === null ? "Create rule" : "Edit rule"}</h3>
                    <p className="settingsCardLead">Changes stay in this mailbox draft until you use Save changes.</p>
                  </div>
                  <div className="settingsCardActions">
                    {resolvedRuleIndex !== null ? (
                      <button className="ghostButton compactButton" type="button" onClick={() => handleDeleteMailFailRule(options.scope, resolvedRuleIndex)}>
                        Delete rule
                      </button>
                    ) : null}
                  </div>
                </div>

                <div className="mailFailEditorGrid" key={`${options.scope}-${resolvedRuleIndex ?? "new"}`}>
                  <label className="settingsField">
                    <span>Name</span>
                    <input
                      value={editorDraft.name}
                      onChange={(event) =>
                        updateMailFailRuleEditor(options.scope, (current) => ({ ...current, name: event.target.value }))
                      }
                    />
                    <small>Shown in the UI. If empty, the trigger becomes the display name.</small>
                  </label>

                  <label className="settingsField">
                    <span>Trigger</span>
                    <input
                      value={editorDraft.trigger}
                      onChange={(event) =>
                        updateMailFailRuleEditor(options.scope, (current) => ({ ...current, trigger: event.target.value }))
                      }
                      placeholder="mf-greylist"
                    />
                    <small>Example recipient: <code>{options.exampleRecipient}</code></small>
                  </label>

                  <label className="settingsField">
                    <span>Action</span>
                    <select
                      value={editorDraft.action}
                      onChange={(event) => {
                        const action = event.target.value as MailFailRule["action"];
                        updateMailFailRuleEditor(options.scope, (current) => ({
                          ...current,
                          action,
                          stage: isReportAction(action) ? "data" : current.stage,
                          code: action === "async-bounce" ? 550 : current.code,
                          enhancedCode: action === "async-bounce" ? current.enhancedCode || "5.0.0" : current.enhancedCode,
                          feedbackType: action === "arf" ? current.feedbackType || "abuse" : "",
                          reportRecipientLocalPart: supportsReportRecipientLocalPart(action)
                            ? current.reportRecipientLocalPart
                            : "",
                          message: isReportAction(action) ? "" : current.message
                        }));
                      }}
                    >
                      <option value="reject">Reject</option>
                      <option value="greylist">Greylist</option>
                      <option value="arf">ARF report</option>
                      <option value="xarf-v3">XARF v3 report</option>
                      <option value="xarf-v4">XARF v4 report</option>
                      <option value="original-report">Original message report</option>
                      <option value="async-bounce">Asynchronous bounce</option>
                    </select>
                  </label>

                  {editorDraft.action === "arf" ? (
                    <label className="settingsField">
                      <span>Feedback type</span>
                      <select
                        value={editorDraft.feedbackType || "abuse"}
                        onChange={(event) =>
                          updateMailFailRuleEditor(options.scope, (current) => ({
                            ...current,
                            feedbackType: event.target.value as MailFailRule["feedbackType"]
                          }))
                        }
                      >
                        <option value="abuse">Abuse</option>
                        <option value="fraud">Fraud</option>
                        <option value="virus">Virus</option>
                        <option value="other">Other</option>
                        <option value="not-spam">Not spam</option>
                      </select>
                      <small>Written to the RFC 5965 <code>Feedback-Type</code> field.</small>
                    </label>
                  ) : null}

                  {supportsReportRecipientLocalPart(editorDraft.action) ? (
                    <label className="settingsField">
                      <span>Report recipient local part</span>
                      <input
                        value={editorDraft.reportRecipientLocalPart}
                        maxLength={64}
                        onChange={(event) =>
                          updateMailFailRuleEditor(options.scope, (current) => ({
                            ...current,
                            reportRecipientLocalPart: event.target.value
                          }))
                        }
                        placeholder="fbl"
                      />
                      <small>
                        Local part only. The domain is taken from the original envelope sender; for example, <code>fbl</code> sends to <code>fbl@sender-domain</code>.
                      </small>
                    </label>
                  ) : null}

                  {!isReportAction(editorDraft.action) ? (
                    <label className="settingsField">
                      <span>Stage</span>
                      <select
                        value={editorDraft.stage}
                        onChange={(event) =>
                          updateMailFailRuleEditor(options.scope, (current) => ({
                            ...current,
                            stage: event.target.value as MailFailRule["stage"]
                          }))
                        }
                      >
                        <option value="mailfrom">MAIL FROM</option>
                        <option value="rcpt">RCPT TO</option>
                        <option value="data">DATA</option>
                      </select>
                    </label>
                  ) : null}

                  {!isReportAction(editorDraft.action) || editorDraft.action === "async-bounce" ? (
                    <>
                      <label className="settingsField">
                        <span>{editorDraft.action === "async-bounce" ? "Diagnostic SMTP code" : "SMTP code"}</span>
                        <input
                          type="number"
                          min={editorDraft.action === "async-bounce" ? 500 : 400}
                          max={599}
                          value={editorDraft.code}
                          onChange={(event) =>
                            updateMailFailRuleEditor(options.scope, (current) => ({
                              ...current,
                              code: Number.parseInt(event.target.value || "550", 10)
                            }))
                          }
                        />
                      </label>

                      <label className="settingsField">
                        <span>Enhanced status code</span>
                        <input
                          value={editorDraft.enhancedCode}
                          onChange={(event) =>
                            updateMailFailRuleEditor(options.scope, (current) => ({ ...current, enhancedCode: event.target.value }))
                          }
                          placeholder="4.7.1 or 5.1.1"
                        />
                      </label>
                    </>
                  ) : null}

                  {!isReportAction(editorDraft.action) ? (
                    <label className="settingsField mailFailMessageField">
                      <span>Reply message</span>
                      <input
                        value={editorDraft.message}
                        onChange={(event) =>
                          updateMailFailRuleEditor(options.scope, (current) => ({ ...current, message: event.target.value }))
                        }
                        placeholder="Try again later"
                      />
                    </label>
                  ) : editorDraft.action === "async-bounce" ? (
                    <label className="settingsField mailFailMessageField">
                      <span>Bounce diagnostic text</span>
                      <input
                        value={editorDraft.message}
                        maxLength={700}
                        pattern="[\x20-\x7E]*"
                        onChange={(event) =>
                          updateMailFailRuleEditor(options.scope, (current) => ({ ...current, message: event.target.value }))
                        }
                        placeholder="Delivery failed after the message was accepted by MailTail."
                      />
                      <small>Used in both the human-readable DSN part and its <code>Diagnostic-Code</code>. Printable US-ASCII, up to 700 characters. Leave empty for the default.</small>
                    </label>
                  ) : (
                    <div className="settingsField mailFailMessageField">
                      <span>Post-accept action</span>
                      <small>MailTail runs this action after accepting DATA and generates the standard report text automatically.</small>
                    </div>
                  )}

                  {editorDraft.action === "greylist" ? (
                    <>
                      <label className="settingsField">
                        <span>Allow after</span>
                        <input
                          type="number"
                          min={1}
                          step={1}
                          value={editorDraft.allowAfter}
                          onChange={(event) =>
                            updateMailFailRuleEditor(options.scope, (current) => ({
                              ...current,
                              allowAfter: Math.max(1, Number.parseInt(event.target.value || "1", 10))
                            }))
                          }
                        />
                        <small>Reject this many matching attempts before allowing delivery.</small>
                      </label>

                      <label className="settingsField">
                        <span>Minimum retry delay</span>
                        <input
                          value={editorDraft.minRetryAfter}
                          onChange={(event) =>
                            updateMailFailRuleEditor(options.scope, (current) => ({ ...current, minRetryAfter: event.target.value }))
                          }
                          placeholder="5m"
                        />
                        <small>Leave empty to allow the retry immediately after the attempt threshold.</small>
                      </label>

                      <label className="settingsField">
                        <span>Reset after</span>
                        <input
                          value={editorDraft.resetAfter}
                          onChange={(event) =>
                            updateMailFailRuleEditor(options.scope, (current) => ({ ...current, resetAfter: event.target.value }))
                          }
                          placeholder="1h"
                        />
                        <small>Defaults to 1h if left empty.</small>
                      </label>
                    </>
                  ) : null}
                </div>
              </section>
          </div>
        ) : (
          <div className="emptyListCard rulesDisabledState">
            <strong>No rule processing</strong>
            <p>Enable MailFail above to configure SMTP responses and post-accept reports.</p>
          </div>
        )}
      </div>
    );
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
      { key: "raw", label: "Raw RFC822" },
      ...(selectedMessage?.attachments?.length ? [{ key: "attachments" as const, label: "Attachments" }] : [])
    ];
  }, [selectedMessage]);

  useEffect(() => {
    if (availableTabs.some((tab) => tab.key === activeTab)) {
      return;
    }
    setActiveTab(availableTabs[0]?.key ?? "text");
  }, [activeTab, availableTabs]);

  useEffect(() => {
    if (settingsOpen) {
      userMenuRef.current?.removeAttribute("open");
    }
  }, [settingsOpen]);

  useEffect(() => {
    if (!settingsOpen) {
      return;
    }

    let activeScope: MailFailSettingsScope | null = null;

    if (session?.isAdmin) {
      if (activeSettingsTab === "adminMailbox" && adminMailboxSubTab === "rules") {
        activeScope = "adminMailbox";
      } else if (activeSettingsTab === "users" && userEditorSubTab === "rules" && selectedManagedUserId) {
        activeScope = "user";
      }
    } else if (activeSettingsTab === "delivery") {
      activeScope = "user";
    }

    if (!activeScope) {
      return;
    }

    const activeRules = (activeScope === "adminMailbox" ? adminMailboxDraft : settingsDraft).mailFailRules ?? [];
    const selectedRule = selectedMailFailRuleIndex !== null ? activeRules[selectedMailFailRuleIndex] : null;
    const scopeChanged = mailFailManagerScope !== activeScope;
    const invalidSelection = selectedMailFailRuleIndex !== null && !selectedRule;

    if (scopeChanged || invalidSelection) {
      activateMailFailWorkspace(activeScope, activeRules.length > 0 ? { selectedIndex: 0 } : { createNew: true });
    }
  }, [
    activeSettingsTab,
    adminMailboxDraft,
    adminMailboxSubTab,
    mailFailManagerScope,
    selectedMailFailRuleIndex,
    selectedManagedUserId,
    session,
    settingsDraft,
    settingsOpen,
    userEditorSubTab
  ]);

  const hasMessages = messages.length > 0;
  const hasAttachments = Boolean(selectedMessage?.attachments?.length);
  const statusTone = error ? "issue" : loading ? "syncing" : "connected";
  const statusLabel = error ? "Sync issue" : loading ? "Syncing" : "Connected";
  const smtpExampleDomain = useMemo(
    () =>
      resolveExampleDomain([
        selectedManagedUser?.settings,
        settingsDraft,
        currentSettings,
        adminMailboxDraft,
        ...managedUsers.map((user) => user.settings)
      ]),
    [adminMailboxDraft, currentSettings, managedUsers, selectedManagedUser, settingsDraft]
  );
  const mailFailExampleDomain = useMemo(
    () =>
      resolveExampleDomain([
        mailFailManagerScope === "adminMailbox" ? adminMailboxDraft : settingsDraft,
        selectedManagedUser?.settings,
        currentSettings,
        adminMailboxDraft,
        ...managedUsers.map((user) => user.settings)
      ]),
    [adminMailboxDraft, currentSettings, mailFailManagerScope, managedUsers, selectedManagedUser, settingsDraft]
  );
  const smtpExampleRecipient = `test@${smtpExampleDomain}`;
  const smtpExampleSender = `sender@${smtpExampleDomain}`;
  const mailFailExampleRecipient = `user+mf-greylist@${mailFailExampleDomain}`;
  const paneCopyValue = currentPaneCopyValue(activeTab, selectedMessage);
  const paneCopyLabel = activeTab === "attachments" ? "Copy names" : "Copy all";
  const hasActiveSearch = query.length > 0 || selectedTag.length > 0;
  const adminSettingsTabs: Array<{ key: SettingsTab; label: string; description: string }> = [
    { key: "instance", label: "Instance", description: "Platform and diagnostics" },
    { key: "adminMailbox", label: "Admin mailbox", description: "Delivery policy and reports" },
    { key: "users", label: "Users & mailboxes", description: "Local accounts and policies" }
  ];
  const userSettingsTabs: Array<{ key: SettingsTab; label: string; description: string }> = [
    { key: "delivery", label: "Delivery & rules", description: "SMTP policy and MailFail" },
    { key: "retention", label: "Retention", description: "Message lifecycle" }
  ];
  const settingsTabs = session?.isAdmin ? adminSettingsTabs : userSettingsTabs;
  const settingsSubTabs: Array<{ key: SettingsSubTab; label: string }> = [
    { key: "general", label: "General" },
    { key: "rules", label: "Rules" }
  ];
  const enabledAdminMailboxFilters = [
    adminMailboxLimitRemoteIps,
    adminMailboxLimitAcceptedRcptDomains,
    adminMailboxLimitAcceptedFromDomains
  ].filter(Boolean).length;
  const enabledUserFilters = [limitAllowedRemoteIps, limitAcceptedRcptDomains, limitAcceptedFromDomains].filter(Boolean).length;
  const activeSettingsContext = (() => {
    if (!session?.isAdmin) {
      return activeSettingsTab === "retention"
        ? { title: "Retention", description: "Control the lifecycle of captured messages." }
        : { title: "Delivery & rules", description: "Manage the complete policy for your mailbox." };
    }
    if (activeSettingsTab === "instance") {
      return { title: "Instance", description: "Platform-wide access and SMTP diagnostics." };
    }
    if (activeSettingsTab === "adminMailbox") {
      return {
        title: adminMailboxSubTab === "rules" ? "Admin mailbox rules" : "Admin mailbox policy",
        description:
          adminMailboxSubTab === "rules"
            ? "Edit report and SMTP response rules, then save the mailbox once."
            : "Configure delivery boundaries and retention for the admin mailbox."
      };
    }
    return {
      title: selectedManagedUserId ? managedUsername || "User mailbox" : "Users & mailboxes",
      description: selectedManagedUserId
        ? userEditorSubTab === "rules"
          ? "Edit this user's rules and save the complete mailbox once."
          : "Manage credentials, delivery boundaries and retention."
        : "Select a user or create a new local mailbox."
    };
  })();
  const canSaveActiveSettings =
    !settingsLoading &&
    !settingsSaving &&
    (!session?.isAdmin || activeSettingsTab !== "users" || selectedManagedUserId !== null);
  const selectedMessageBadges = selectedMessage ? buildMessageBadges(selectedMessage) : [];
  const repoUrl = "https://github.com/vquie/MailTail";
  const issuesUrl = "https://github.com/vquie/MailTail/issues/new/choose";
  const sidebarDiagnostics = [
    { label: "Messages", value: String(stats.messageCount) },
    { label: "Database size", value: formatBytes(stats.totalSize) },
    { label: "Last received", value: stats.latestReceivedAt ? formatDate(stats.latestReceivedAt) : "Waiting" },
    { label: "Version", value: version || "-" },
    { label: "Session", value: session?.username || "-" },
    { label: "Auto refresh", value: autoRefreshEnabled ? "15s" : "Paused" },
    { label: "Active tag", value: selectedTag || "All" }
  ];

  return (
    <div className={settingsOpen ? "shell settingsMode" : "shell"}>
      <header className="workspaceHeader">
        <div className="workspaceHeaderCard">
          <div className="workspaceTopRow">
            <a className="brandLockup brandHomeLink" href="/" onClick={() => setSettingsOpen(false)}>
              <div className="brandMark" aria-hidden="true">
                <MailIcon />
              </div>
              <div className="workspaceTitle">
                <p className="eyebrow">SMTP Debugger</p>
                <h1>MailTail</h1>
              </div>
            </a>

            <label className="searchField workspaceSearchField searchFieldLarge">
              <span className="searchFieldIcon" aria-hidden="true">
                <SearchIcon />
              </span>
              <input
                aria-label="Search subject, from, to"
                value={queryInput}
                onChange={(event) => setQueryInput(event.target.value)}
                placeholder="Search messages, senders, recipients"
              />
            </label>

            <div className="topToolbar">
              <div className={`toolbarStatus toolbarStatus${statusTone === "connected" ? "Success" : statusTone === "syncing" ? "Warning" : "Danger"}`}>
                <span className="toolbarStatusDot" aria-hidden="true" />
                <span>{statusLabel}</span>
              </div>
              <label className="toolbarToggle">
                <input
                  type="checkbox"
                  checked={autoRefreshEnabled}
                  onChange={(event) => setAutoRefreshEnabled(event.target.checked)}
                />
                <span>Auto refresh</span>
              </label>
              <button
                className="primaryButton compactButton"
                onClick={() =>
                  void loadOverview(queryRef.current, {
                    preferredId: selectedIdRef.current,
                    forceDetail: true
                  })
                }
              >
                Refresh
              </button>
              <div className="topToolbarAccountSlot">
                <details className="userMenu" ref={userMenuRef}>
                  <summary className="ghostButton compactButton userMenuTrigger">
                    <span>{session?.username || "User"}</span>
                    <span className="userMenuCaret" aria-hidden="true">▼</span>
                  </summary>
                  <div className="userMenuPanel">
                    <button className="ghostButton compactButton toolbarButton userMenuAction" type="button" onClick={openAboutPanel}>
                      <InfoIcon />
                      <span>About</span>
                    </button>
                    <a className="ghostButton compactButton toolbarButton userMenuAction" href="/api/docs" target="_blank" rel="noreferrer">
                      <DocsIcon />
                      <span>API Docs</span>
                    </a>
                    <button className="ghostButton compactButton toolbarButton userMenuAction" type="button" onClick={() => void openSettingsPanel()}>
                      <SettingsIcon />
                      <span>Settings</span>
                    </button>
                    <button className="ghostButton compactButton" type="button" onClick={() => void handleLogout()}>
                      Logout
                    </button>
                  </div>
                </details>
              </div>
            </div>
          </div>
        </div>
      </header>

      <aside className="sidebar">
        <div className="sidebarSectionHeader">
          <div>
            <p className="eyebrow">Captured messages</p>
            <h2>Inbox</h2>
          </div>
          <div className="sidebarSectionMeta">
            <span>{messages.length} loaded</span>
            <span>{hasMore ? "More available" : "Live tail"}</span>
          </div>
        </div>

        <div className="tagFilterPanel">
          <div className="tagFilterHeader">
            <span>Tags</span>
            <span>{availableTags.length ? `${availableTags.length} available` : "No plus tags yet"}</span>
          </div>
          <div className="tagFilterList" role="list" aria-label="Message tags">
            <button
              className={selectedTag === "" ? "tagFilterChip active" : "tagFilterChip"}
              type="button"
              onClick={() => setSelectedTag("")}
            >
              All mail
            </button>
            {availableTags.map((tag) => (
              <button
                key={tag}
                className={selectedTag === tag ? "tagFilterChip active" : "tagFilterChip"}
                type="button"
                onClick={() => setSelectedTag(tag)}
                style={buildTagColorStyle(tag)}
              >
                +{tag}
              </button>
            ))}
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
              <div className="messageMetaCompact">
                <span className="messageLine">{message.headerFrom || message.mailFrom}</span>
                <span className="messageRecipient">{primaryRecipient(message)}</span>
              </div>
              <div className="messageBadgeRow">
                {buildMessageBadges(message).map((badge) => (
                  <span
                    key={badge.label}
                    className={badge.tag ? "messageBadge messageBadgeTag" : `messageBadge messageBadge${badge.tone}`}
                    style={badge.tag ? buildTagColorStyle(badge.tag) : undefined}
                  >
                    {badge.icon}
                    <span>{badge.label}</span>
                  </span>
                ))}
              </div>
            </button>
          ))}
          {!loading && messages.length === 0 ? (
            <div className="emptyListCard">
              <strong>{hasActiveSearch ? "No matching messages" : "Inbox is empty"}</strong>
              <p>{hasActiveSearch ? describeEmptyState(query, selectedTag) : "No messages captured yet."}</p>
            </div>
          ) : null}
          {hasMore ? (
            <button className="ghostButton compactButton loadMoreButton" disabled={loadingMore} onClick={() => void handleLoadMore()}>
              {loadingMore ? "Loading..." : "Load more"}
            </button>
          ) : null}
        </div>

        <div className="sidebarFooter">
          <div className={sidebarDiagnosticsOpen ? "sidebarUtilityPanel open" : "sidebarUtilityPanel"}>
            <button
              className={sidebarDiagnosticsOpen ? "ghostButton compactButton sidebarUtilityToggle open" : "ghostButton compactButton sidebarUtilityToggle"}
              type="button"
              aria-expanded={sidebarDiagnosticsOpen}
              aria-label={sidebarDiagnosticsOpen ? "Hide details" : "Show details"}
              onClick={() => setSidebarDiagnosticsOpen((current) => !current)}
            >
              <span className="sidebarUtilityMeta">
                <span className="sidebarUtilityLabel">Details</span>
                <ChevronIcon className="sidebarUtilityChevron" />
              </span>
            </button>

            {sidebarDiagnosticsOpen ? (
              <div className="sidebarUtilityContent">
                <section className="diagnosticsCard">
                  <div className="diagnosticsCardHeader">
                    <div>
                      <p className="eyebrow">Runtime</p>
                      <h3>Diagnostics</h3>
                    </div>
                    <span className="diagnosticsStamp">{lastSyncAt ? `Synced ${formatTime(lastSyncAt)}` : "Waiting"}</span>
                  </div>
                  <dl className="diagnosticsList">
                    {sidebarDiagnostics.map((item) => (
                      <div key={item.label}>
                        <dt>{item.label}</dt>
                        <dd>{item.value}</dd>
                      </div>
                    ))}
                  </dl>
                </section>
              </div>
            ) : null}
          </div>
          {!session?.isAdmin && settingsLoaded && currentSettings.mailFailEnabled ? (
            <div className="sidebarNotice">
              <span className="statusDot" aria-hidden="true" />
              <span>MailFail rules active for this mailbox</span>
            </div>
          ) : null}
          <button className="dangerButton compactButton" disabled={!hasMessages} onClick={() => void handleClearInbox()}>
            Delete all messages
          </button>
        </div>
      </aside>

      <main className={settingsOpen ? "contentPane settingsPagePane" : "contentPane"}>
        {!settingsOpen && selectedMessage ? (
          <section className="messageWorkspace">
            <div className={messageDetailsOpen ? "messageDetailPanel open" : "messageDetailPanel"}>
              <div className="messageDetailSummary">
                <div className="messageDetailSummaryCopy">
                  <p className="eyebrow">Message</p>
                  <h2>{selectedMessage.subject || "(no subject)"}</h2>
                </div>
                <button
                  className={messageDetailsOpen ? "ghostButton compactButton sidebarUtilityToggle messageDetailToggle open" : "ghostButton compactButton sidebarUtilityToggle messageDetailToggle"}
                  type="button"
                  aria-expanded={messageDetailsOpen}
                  aria-label={messageDetailsOpen ? "Hide message details" : "Show message details"}
                  onClick={() => setMessageDetailsOpen((current) => !current)}
                >
                  <span className="sidebarUtilityMeta">
                    <span className="sidebarUtilityLabel">Details</span>
                    <ChevronIcon className="sidebarUtilityChevron" />
                  </span>
                </button>
              </div>

              {messageDetailsOpen ? (
                <div className="heroCard compactHero messageHeroCard">
                  <div className="heroCopy">
                    <div className="messageInfoGrid">
                      <dl className="messageInfoList">
                        <div className="messageInfoRow">
                          <dt>Envelope MAIL FROM</dt>
                          <dd>{selectedMessage.mailFrom || "-"}</dd>
                        </div>
                        <div className="messageInfoRow">
                          <dt>Envelope RCPT TO</dt>
                          <dd>{selectedMessage.rcptTo.join(", ") || primaryRecipient(selectedMessage) || "-"}</dd>
                        </div>
                        <div className="messageInfoRow">
                          <dt>Tags</dt>
                          <dd>
                            {selectedMessage.tags.length ? (
                              <span className="tagDetailList">
                                {selectedMessage.tags.map((tag) => (
                                  <span key={tag} className="tagDetailPill" style={buildTagColorStyle(tag)}>
                                    +{tag}
                                  </span>
                                ))}
                              </span>
                            ) : (
                              "No plus tag"
                            )}
                          </dd>
                        </div>
                        <div className="messageInfoRow">
                          <dt>Remote IP</dt>
                          <dd>{selectedMessage.remoteIp || "-"}</dd>
                        </div>
                      </dl>
                      <dl className="messageInfoList">
                        <div className="messageInfoRow">
                          <dt>HELO</dt>
                          <dd>{selectedMessage.helo || "-"}</dd>
                        </div>
                        <div className="messageInfoRow">
                          <dt>Message size</dt>
                          <dd>{formatBytes(selectedMessage.size)}</dd>
                        </div>
                        <div className="messageInfoRow">
                          <dt>Received</dt>
                          <dd>{formatDate(selectedMessage.receivedAt)}</dd>
                        </div>
                      </dl>
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
                    <button className="dangerButton compactButton" onClick={() => void handleDeleteCurrent()}>
                      Delete message
                    </button>
                  </div>
                </div>
              ) : null}
            </div>

            <div className="detailGrid">
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
                ) : activeTab === "attachments" ? (
                  hasAttachments ? (
                    <div className="attachmentViewport">
                      <div className="attachmentList attachmentListWide">
                        {selectedMessage.attachments?.map((attachment) => (
                          <a
                            key={attachment.id}
                            className="attachmentItem"
                            href={attachmentUrl(selectedMessage.id, attachment.id)}
                          >
                            <div className="attachmentMeta">
                              <strong>{attachment.fileName}</strong>
                              <span>{attachment.contentType}</span>
                            </div>
                            <span className="attachmentStats">
                              {attachment.inline ? "Inline" : "Attachment"} · {formatBytes(attachment.size)}
                            </span>
                          </a>
                        ))}
                      </div>
                    </div>
                  ) : (
                    <p className="emptyState">No attachments available.</p>
                  )
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
            </div>
          </section>
        ) : !settingsOpen ? (
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
        ) : null}

        {!settingsOpen && error ? <div className="errorBanner">{error}</div> : null}

        {settingsOpen ? (
          <div className="settingsOverlay settingsPageOverlay" onClick={() => setSettingsOpen(false)}>
            <section className="settingsPanel settingsPageShell" onClick={(event) => event.stopPropagation()}>
              <div className="settingsPageHeader">
                <div className="settingsPageTitle">
                  <p className="eyebrow">{session?.isAdmin ? "Admin area" : "User settings"}</p>
                  <h2>Settings</h2>
                  <p className="settingsLead">
                    {session?.isAdmin
                      ? "Configure the MailTail instance, the admin mailbox and local users from one structured workspace."
                      : "These settings are saved live and control delivery rules, filters and message retention for your mailbox."}
                  </p>
                </div>
                <div className="settingsPageToolbar">
                  <div className="settingsSupportLinks">
                    <button className="ghostButton compactButton settingsToolbarCloseButton" type="button" onClick={() => setSettingsOpen(false)}>
                      Close
                    </button>
                  </div>
                </div>
              </div>

              {settingsError || settingsNotice ? (
                <div className="flashStack">
                  {settingsError ? <FlashBanner tone="error" message={settingsError} onDismiss={() => setSettingsError(null)} /> : null}
                  {settingsNotice ? <FlashBanner tone="success" message={settingsNotice} onDismiss={() => setSettingsNotice(null)} /> : null}
                </div>
              ) : null}

              <div className="settingsWorkspaceShell">
                <aside className="settingsNavigation" aria-label="Settings navigation">
                  <div className="settingsNavigationHeader">
                    <span>Workspace</span>
                    <small>Choose a context. Your position stays visible while you work.</small>
                  </div>
                  <div className="settingsTabs" role="tablist" aria-label="Settings sections">
                    {settingsTabs.map((tab) => (
                      <button
                        key={tab.key}
                        className={activeSettingsTab === tab.key ? "settingsTabButton active" : "settingsTabButton"}
                        onClick={() => handleSettingsSectionChange(tab.key)}
                        type="button"
                      >
                        <strong>{tab.label}</strong>
                        <small>{tab.description}</small>
                      </button>
                    ))}
                  </div>
                </aside>

                <div className="settingsWorkspaceMain">
                  <div className="settingsContextBar">
                    <div>
                      <span className="settingsContextLabel">Current context</span>
                      <strong>{activeSettingsContext.title}</strong>
                      <small>{activeSettingsContext.description}</small>
                    </div>
                    <button
                      className="dangerButton settingsSaveButton"
                      disabled={!canSaveActiveSettings}
                      onClick={() => void handleSaveActiveSettings()}
                      type="button"
                    >
                      {settingsSaving ? "Saving…" : "Save changes"}
                    </button>
                  </div>

                  {settingsLoading ? <p className="emptyState settingsLoadingState">Loading settings...</p> : null}

              {!settingsLoading && session?.isAdmin ? (
                <div className="adminSettingsLayout">
                  {activeSettingsTab === "instance" ? (
                    <div className="settingsCardGrid settingsCardGridAdmin">
                      <section className="settingsField settingsCard settingsCardPrimary">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Platform settings</h3>
                            <p className="settingsCardLead">Instance-wide configuration for API access and SMTP diagnostics.</p>
                          </div>
                        </div>

                        <div className="settingsCardBody">
                          <label className="settingsField nestedSettingsField">
                            <span>Allowed origins</span>
                            <textarea
                              rows={4}
                              value={globalSettingsDraft.allowedOrigins}
                              onChange={(event) =>
                                setGlobalSettingsDraft((current) => ({
                                  ...current,
                                  allowedOrigins: event.target.value
                                }))
                              }
                            />
                            <small>Instance-wide CORS allow-list. Comma-separated and applied to all users.</small>
                          </label>

                          <div className="settingsField nestedSettingsField">
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
                              <span>Enable detailed SMTP protocol logging for the whole instance</span>
                            </label>
                            <small>Useful while debugging delivery flows and MailFail behavior.</small>
                          </div>
                        </div>
                      </section>

                      <section className="settingsField settingsCard">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Statistics</h3>
                            <p className="settingsCardLead">Current state of the instance.</p>
                          </div>
                        </div>
                        <div className="settingsStatsGrid">
                          <div className="settingsStat">
                            <span>Messages</span>
                            <strong>{stats.messageCount}</strong>
                          </div>
                          <div className="settingsStat">
                            <span>Total size</span>
                            <strong>{formatBytes(stats.totalSize)}</strong>
                          </div>
                          <div className="settingsStat">
                            <span>Latest received</span>
                            <strong>{stats.latestReceivedAt ? formatDate(stats.latestReceivedAt) : "No messages yet"}</strong>
                          </div>
                        </div>
                      </section>

                    </div>
                  ) : null}

                  {activeSettingsTab === "adminMailbox" ? (
                    <div
                      className={
                        adminMailboxSubTab === "rules"
                          ? "settingsCardGrid settingsCardGridAdmin settingsCardGridWide"
                          : "settingsCardGrid settingsCardGridAdmin"
                      }
                    >
                      <section className="settingsField settingsCard settingsCardPrimary">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Admin mailbox</h3>
                            <p className="settingsCardLead">Use this when the environment admin should also receive and retain mail.</p>
                          </div>
                        </div>

                        <div className="settingsCardBody">
                          <div className="settingsSubTabs" role="tablist" aria-label="Admin mailbox sections">
                            {settingsSubTabs.map((tab) => (
                              <button
                                key={tab.key}
                                className={adminMailboxSubTab === tab.key ? "settingsSubTabButton active" : "settingsSubTabButton"}
                                onClick={() => handleSettingsSubTabChange("adminMailbox", tab.key)}
                                type="button"
                              >
                                {tab.label}
                              </button>
                            ))}
                          </div>

                          {adminMailboxSubTab === "general" ? (
                            <div className="settingsField nestedSettingsField">
                              <span>Mailbox activation</span>
                              <label className="toggleRow">
                                <input
                                  type="checkbox"
                                  checked={adminMailboxEnabled}
                                  onChange={(event) => {
                                    setAdminMailboxEnabled(event.target.checked);
                                    if (event.target.checked && adminMailboxDraft.mailFailEnabled && adminMailboxDraft.mailFailRules.length === 0) {
                                      setAdminMailboxDraft((current) => ({
                                        ...current,
                                        mailFailRules: cloneMailFailRules(defaultMailFailRulesTemplate)
                                      }));
                                    }
                                  }}
                                />
                                <span>Enable admin mailbox policies</span>
                              </label>
                              <small>Disabled means the admin account does not capture or retain messages.</small>
                            </div>
                          ) : null}

                          {adminMailboxEnabled && adminMailboxSubTab === "general" ? (
                            <div className="settingsGrid adminMailboxGrid">
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

                          {adminMailboxSubTab === "rules" ? (
                            adminMailboxEnabled ? (
                              renderRulesWorkspace({
                                scope: "adminMailbox",
                                enabled: adminMailboxDraft.mailFailEnabled,
                                onToggle: (enabled) =>
                                  setAdminMailboxDraft((current) => ({
                                    ...current,
                                    mailFailEnabled: enabled,
                                    mailFailRules:
                                      enabled && current.mailFailRules.length === 0
                                        ? cloneMailFailRules(defaultMailFailRulesTemplate)
                                        : current.mailFailRules
                                  })),
                                exampleRecipient: `admin+mf-greylist@${mailFailExampleDomain}`
                              })
                            ) : (
                              <div className="emptyListCard mailboxDisabledState">
                                <strong>Admin mailbox is disabled</strong>
                                <p>Enable the mailbox under General before configuring its rules.</p>
                                <button
                                  className="ghostButton compactButton"
                                  type="button"
                                  onClick={() => setAdminMailboxSubTab("general")}
                                >
                                  Open General
                                </button>
                              </div>
                            )
                          ) : null}
                        </div>
                      </section>

                      {adminMailboxSubTab === "rules" ? null : (
                        <section className="settingsField settingsCard">
                          <div className="settingsCardHeader">
                            <div>
                              <h3>Mailbox summary</h3>
                              <p className="settingsCardLead">Quick state of the admin-specific delivery rules.</p>
                            </div>
                          </div>
                          <dl className="settingsDefinitionList">
                            <div>
                              <dt>Status</dt>
                              <dd>{adminMailboxEnabled ? "Enabled" : "Disabled"}</dd>
                            </div>
                            <div>
                              <dt>MailFail rules</dt>
                              <dd>{adminMailboxDraft.mailFailEnabled ? adminMailboxDraft.mailFailRules.length : 0}</dd>
                            </div>
                            <div>
                              <dt>Active filters</dt>
                              <dd>{enabledAdminMailboxFilters}</dd>
                            </div>
                            <div>
                              <dt>Auto-delete</dt>
                              <dd>{adminMailboxAutoDeleteEnabled ? `${adminMailboxDraft.autoDeleteAfterDays || defaultAutoDeleteDays} days` : "Off"}</dd>
                            </div>
                          </dl>
                        </section>
                      )}
                    </div>
                  ) : null}

                  {activeSettingsTab === "users" ? (
                    <div className="adminUserLayout">
                      <section className="settingsField settingsCard adminUsersCard">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Users</h3>
                            <p className="settingsCardLead">Select an existing user or create a new local mailbox.</p>
                          </div>
                          <button className="ghostButton compactButton" onClick={handleCreateManagedUser} type="button">
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
                                  setUserEditorSubTab("general");
                                  applyManagedUserDraft(user);
                                  setMailFailManagerScope(null);
                                  setSelectedMailFailRuleIndex(null);
                                  setMailFailRuleDraft(createDefaultMailFailRule());
                                }}
                                type="button"
                              >
                                <strong>{user.username}</strong>
                                <span className="mutedText">{user.settings.acceptedRcptDomains || "No recipient filter"}</span>
                              </button>
                            ))
                          ) : (
                            <div className="emptyListCard">
                              <strong>No users yet</strong>
                              <p>Create the first local user to start assigning mailbox policies.</p>
                            </div>
                          )}
                        </div>
                      </section>

                      <section className="settingsField settingsCard adminEditorCard">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>{selectedManagedUserId ? "User policies" : "User editor"}</h3>
                            <p className="settingsCardLead">
                              {selectedManagedUserId ? "Update login and delivery policy for the selected user." : "Select a user on the left or create one first."}
                            </p>
                          </div>
                          <div className="settingsCardActions">
                            {selectedManagedUserId ? (
                              <button
                                className="ghostButton compactButton"
                                disabled={settingsSaving}
                                onClick={() => void handleDeleteManagedUser()}
                                type="button"
                              >
                                Delete user
                              </button>
                            ) : null}
                          </div>
                        </div>

                        {selectedManagedUserId ? (
                          <>
                            <div className="settingsSubTabs" role="tablist" aria-label="User editor sections">
                              {settingsSubTabs.map((tab) => (
                                <button
                                  key={tab.key}
                                  className={userEditorSubTab === tab.key ? "settingsSubTabButton active" : "settingsSubTabButton"}
                                  onClick={() => handleSettingsSubTabChange("user", tab.key)}
                                  type="button"
                                >
                                  {tab.label}
                                </button>
                              ))}
                            </div>

                            {userEditorSubTab === "general" ? (
                              <div className="settingsGrid adminEditorGrid">
                                <label className="settingsField">
                                  <span>Username</span>
                                  <input value={managedUsername} onChange={(event) => setManagedUsername(event.target.value)} />
                                  <small>Local username used for login.</small>
                                </label>
                                <label className="settingsField">
                                  <span>New password</span>
                                  <input
                                    type="password"
                                    value={managedPassword}
                                    onChange={(event) => setManagedPassword(event.target.value)}
                                    placeholder="Leave empty to keep the current password"
                                  />
                                  <small>Optional on update.</small>
                                </label>

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
                                    <input
                                      type="checkbox"
                                      checked={limitAcceptedRcptDomains}
                                      onChange={(event) => setLimitAcceptedRcptDomains(event.target.checked)}
                                    />
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
                                    <input
                                      type="checkbox"
                                      checked={limitAcceptedFromDomains}
                                      onChange={(event) => setLimitAcceptedFromDomains(event.target.checked)}
                                    />
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
                            ) : null}

                            {userEditorSubTab === "rules" ? (
                              renderRulesWorkspace({
                                scope: "user",
                                enabled: settingsDraft.mailFailEnabled,
                                onToggle: handleToggleMailFail,
                                exampleRecipient: mailFailExampleRecipient
                              })
                            ) : null}
                          </>
                        ) : (
                          <div className="emptyListCard adminEditorEmpty">
                            <strong>No user selected</strong>
                            <p>Create a user from the left column, then edit mailbox policies here.</p>
                          </div>
                        )}
                      </section>
                    </div>
                  ) : null}
                </div>
              ) : null}

              {!settingsLoading && !session?.isAdmin ? (
                <div className="settingsCardGrid">
                  {activeSettingsTab === "delivery" ? (
                    <>
                      <section className="settingsField settingsCard settingsCardPrimary">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Delivery policies</h3>
                            <p className="settingsCardLead">Control MailFail and sender or recipient restrictions for your mailbox.</p>
                          </div>
                        </div>

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
                            {settingsDraft.mailFailEnabled ? (
                              <>
                                <div className="ruleSummaryList">
                                  {(settingsDraft.mailFailRules ?? []).length ? (
                                    settingsDraft.mailFailRules.map((rule) => (
                                      <span key={`${rule.name}-${rule.trigger}-${rule.stage}`} className="ruleChip">
                                        {rule.name || rule.trigger}
                                      </span>
                                    ))
                                  ) : (
                                    <span className="mutedText">No rules configured yet.</span>
                                  )}
                                </div>
                                <div className="settingsCardActions">
                                  <button className="ghostButton compactButton" type="button" onClick={() => handleImportMailFailTemplate("user")}>
                                    Load example rules
                                  </button>
                                </div>
                              </>
                            ) : null}
                          </div>

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
                        </div>
                      </section>

                      <section className="settingsField settingsCard">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Policy summary</h3>
                            <p className="settingsCardLead">Quick overview of the currently configured delivery rules.</p>
                          </div>
                        </div>
                        <dl className="settingsDefinitionList">
                          <div>
                            <dt>MailFail</dt>
                            <dd>{settingsDraft.mailFailEnabled ? `${settingsDraft.mailFailRules.length} rules` : "Off"}</dd>
                          </div>
                          <div>
                            <dt>Active filters</dt>
                            <dd>{enabledUserFilters}</dd>
                          </div>
                          <div>
                            <dt>Retention</dt>
                            <dd>{autoDeleteEnabled ? `${settingsDraft.autoDeleteAfterDays || defaultAutoDeleteDays} days` : "Manual"}</dd>
                          </div>
                        </dl>
                      </section>
                    </>
                  ) : null}

                  {activeSettingsTab === "retention" ? (
                    <>
                      <section className="settingsField settingsCard settingsCardPrimary">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Retention</h3>
                            <p className="settingsCardLead">Configure how long captured messages stay in your mailbox.</p>
                          </div>
                        </div>

                        <div className="settingsField nestedSettingsField toggleField">
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
                      </section>

                      <section className="settingsField settingsCard">
                        <div className="settingsCardHeader">
                          <div>
                            <h3>Mailbox summary</h3>
                            <p className="settingsCardLead">Current retention-relevant mailbox values.</p>
                          </div>
                        </div>
                        <dl className="settingsDefinitionList">
                          <div>
                            <dt>Messages</dt>
                            <dd>{stats.messageCount}</dd>
                          </div>
                          <div>
                            <dt>Mailbox size</dt>
                            <dd>{formatBytes(stats.totalSize)}</dd>
                          </div>
                          <div>
                            <dt>Retention mode</dt>
                            <dd>{autoDeleteEnabled ? `Auto-delete after ${settingsDraft.autoDeleteAfterDays || defaultAutoDeleteDays} days` : "Keep until manual deletion"}</dd>
                          </div>
                        </dl>
                      </section>
                    </>
                  ) : null}
                </div>
              ) : null}
                </div>
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

        {aboutOpen ? (
          <div className="settingsOverlay" onClick={() => setAboutOpen(false)}>
            <section className="settingsPanel aboutPanel" onClick={(event) => event.stopPropagation()}>
              <div className="settingsPanelHeader">
                <div className="settingsPageTitle">
                  <p className="eyebrow">About</p>
                  <h2>MailTail</h2>
                  <p className="settingsLead">Useful runtime details for the current workspace.</p>
                </div>
                <button className="ghostButton compactButton settingsToolbarCloseButton" type="button" onClick={() => setAboutOpen(false)}>
                  Close
                </button>
              </div>

              <section className="settingsField settingsCard">
                <dl className="settingsDefinitionList">
                  <div>
                    <dt>Version</dt>
                    <dd>{version || "-"}</dd>
                  </div>
                  <div>
                    <dt>Signed in as</dt>
                    <dd>{session?.username || "-"}</dd>
                  </div>
                  <div>
                    <dt>Local users</dt>
                    <dd>{managedUsers.length}</dd>
                  </div>
                  <div>
                    <dt>Admin mailbox</dt>
                    <dd>{adminMailboxEnabled ? "Enabled" : "Disabled"}</dd>
                  </div>
                </dl>
                <div className="settingsCardActions">
                  <a className="ghostButton compactButton toolbarButton settingsSupportLink" href={repoUrl} target="_blank" rel="noreferrer">
                    <GitHubIcon />
                    <span>vquie/MailTail</span>
                  </a>
                  <a className="ghostButton compactButton toolbarButton settingsSupportLink" href={issuesUrl} target="_blank" rel="noreferrer">
                    <GitHubIcon />
                    <span>Report a bug</span>
                  </a>
                </div>
              </section>
            </section>
          </div>
        ) : null}

      </main>
    </div>
  );
}

function FlashBanner({ tone, message, onDismiss }: { tone: FlashTone; message: string; onDismiss: () => void }) {
  return (
    <div className={tone === "error" ? "flashBanner flashBannerError" : "flashBanner flashBannerSuccess"} role="alert" aria-live="polite">
      <span>{message}</span>
      <button type="button" className="flashBannerClose" onClick={onDismiss} aria-label="Dismiss notification">
        ×
      </button>
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
    case "attachments":
      return (message.attachments ?? [])
        .map((attachment) => `${attachment.fileName} (${attachment.contentType}, ${formatBytes(attachment.size)})`)
        .join("\n");
    default:
      return "";
  }
}

type MessageBadge = {
  label: string;
  tone: "Neutral" | "Info" | "Success" | "Warning" | "Danger";
  icon: ReturnType<typeof HtmlIcon> | null;
  tag?: string;
};

function buildMessageBadges(message: Message): MessageBadge[] {
  const badges: MessageBadge[] = [];
  const hasHtml = Boolean(message.htmlBody?.trim());
  const hasText = Boolean(message.textBody?.trim());
  const attachments = message.attachments ?? [];
  const inlineImages = attachments.filter((attachment) => attachment.inline).length;
  const tags = message.tags ?? [];

  if (tags.length > 0) {
    badges.push({ label: `+${tags[0]}`, tone: "Info", icon: null, tag: tags[0] });
    if (tags.length > 1) {
      badges.push({ label: `+${tags.length - 1} more`, tone: "Neutral", icon: null });
    }
  }

  if (hasHtml) {
    badges.push({ label: "HTML", tone: "Info", icon: <HtmlIcon /> });
  }
  if (hasText) {
    badges.push({ label: "Text", tone: "Neutral", icon: <TextIcon /> });
  }
  if (attachments.length > 0) {
    badges.push({ label: "Attachment", tone: "Warning", icon: <AttachmentIcon /> });
  }
  if (inlineImages > 0) {
    badges.push({ label: "Inline Images", tone: "Warning", icon: <ImageIcon /> });
  }
  if ((hasHtml && hasText) || attachments.length > 0) {
    badges.push({ label: "Multipart", tone: "Success", icon: null });
  }

  return badges;
}

function primaryRecipient(message: Message): string {
  return message.headerTo || message.rcptTo[0] || "-";
}

function describeEmptyState(query: string, tag: string): string {
  const filters: string[] = [];
  if (query) {
    filters.push(`search "${query}"`);
  }
  if (tag) {
    filters.push(`tag "+${tag}"`);
  }
  if (filters.length === 0) {
    return "No messages captured yet.";
  }
  return `No results for ${filters.join(" and ")}.`;
}

function describeDeleteScope(query: string, tag: string): string {
  const filters: string[] = [];
  if (query) {
    filters.push(`search "${query}"`);
  }
  if (tag) {
    filters.push(`tag "+${tag}"`);
  }
  if (filters.length === 0) {
    return "Delete all messages in the current mailbox?";
  }
  return `Delete all messages matching ${filters.join(" and ")}?`;
}

function buildTagColorStyle(tag: string): CSSProperties {
  const normalized = tag.trim().toLowerCase();
  const hash = hashString(normalized)
  const hue = hash % 360;
  const borderHue = (hue + 12) % 360;

  return {
    "--tag-bg": `hsla(${hue} 70% 56% / 0.18)`,
    "--tag-border": `hsla(${borderHue} 78% 68% / 0.34)`,
    "--tag-text": `hsl(${hue} 85% 82%)`
  } as CSSProperties;
}

function hashString(value: string): number {
  let hash = 0;
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 31 + value.charCodeAt(index)) >>> 0;
  }
  return hash;
}

function normalizeSettingsDraft(settings: AppSettings): AppSettings {
  return {
    ...settings,
    reportFrom: settings.reportFrom ?? "",
    mailFailRules: cloneMailFailRules(settings.mailFailRules ?? []),
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
    mailFailRules: cloneMailFailRules(settings.mailFailRules ?? []),
    allowedRemoteIps: toggles.limitAllowedRemoteIps ? settings.allowedRemoteIps : "",
    acceptedRcptDomains: toggles.limitAcceptedRcptDomains ? settings.acceptedRcptDomains : "",
    acceptedFromDomains: toggles.limitAcceptedFromDomains ? settings.acceptedFromDomains : "",
    autoDeleteAfterDays: toggles.autoDeleteEnabled ? Math.max(1, settings.autoDeleteAfterDays || defaultAutoDeleteDays) : 0
  };
}

function resolveExampleDomain(settingsList: Array<AppSettings | null | undefined>): string {
  for (const settings of settingsList) {
    const domain = extractExampleDomain(settings);
    if (domain) {
      return domain;
    }
  }
  return "example.test";
}

function extractExampleDomain(settings?: AppSettings | null): string | null {
  if (!settings) {
    return null;
  }

  for (const value of [settings.acceptedRcptDomains, settings.acceptedFromDomains]) {
    for (const token of splitCSV(value)) {
      const domain = normalizeExampleDomainToken(token);
      if (domain) {
        return domain;
      }
    }
  }

  return null;
}

function splitCSV(value: string): string[] {
  if (!value.trim()) {
    return [];
  }

  return value
    .split(/[\n\r,]+/)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function normalizeExampleDomainToken(token: string): string | null {
  const normalized = token.trim().toLowerCase();
  if (!normalized) {
    return null;
  }

  if (/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(normalized)) {
    return normalized;
  }

  const atIndex = normalized.lastIndexOf("@");
  if (atIndex > 0) {
    const candidate = normalized.slice(atIndex + 1);
    if (/^[a-z0-9-]+(\.[a-z0-9-]+)+$/.test(candidate)) {
      return candidate;
    }
  }

  return null;
}

function cloneMailFailRule(rule: MailFailRule): MailFailRule {
  return {
    ...emptyMailFailRule,
    ...rule,
    name: rule.name ?? "",
    trigger: rule.trigger ?? "",
    stage: rule.stage ?? emptyMailFailRule.stage,
    action: rule.action ?? emptyMailFailRule.action,
    allowAfter: rule.allowAfter ?? emptyMailFailRule.allowAfter,
    minRetryAfter: rule.minRetryAfter ?? "",
    resetAfter: rule.resetAfter ?? "",
    code: rule.code ?? emptyMailFailRule.code,
    enhancedCode: rule.enhancedCode ?? "",
    feedbackType: rule.feedbackType ?? "",
    reportRecipientLocalPart: rule.reportRecipientLocalPart ?? "",
    message: rule.message ?? ""
  };
}

function cloneMailFailRules(rules: MailFailRule[]): MailFailRule[] {
  return rules.map((rule) => cloneMailFailRule(rule));
}

function createDefaultMailFailRule(): MailFailRule {
  return cloneMailFailRule(emptyMailFailRule);
}

function hasMailFailRuleDraftChanges(rule: MailFailRule): boolean {
  return (
    (rule.name ?? "").trim() !== "" ||
    (rule.trigger ?? "").trim() !== "" ||
    (rule.message ?? "").trim() !== "" ||
    rule.action !== emptyMailFailRule.action ||
    rule.stage !== emptyMailFailRule.stage ||
    rule.code !== emptyMailFailRule.code ||
    (rule.enhancedCode ?? "").trim() !== "" ||
    (rule.feedbackType ?? "").trim() !== "" ||
    (rule.reportRecipientLocalPart ?? "").trim() !== "" ||
    rule.allowAfter !== emptyMailFailRule.allowAfter ||
    (rule.minRetryAfter ?? "").trim() !== "" ||
    (rule.resetAfter ?? "").trim() !== emptyMailFailRule.resetAfter
  );
}

function normalizeMailFailRuleDraft(rule: MailFailRule): MailFailRule {
  const normalizedInput = cloneMailFailRule(rule);
  const reportAction = isReportAction(normalizedInput.action);
  return {
    ...normalizedInput,
    name: normalizedInput.name.trim(),
    trigger: normalizedInput.trigger.trim(),
    stage: reportAction ? "data" : normalizedInput.stage,
    action: normalizedInput.action,
    allowAfter: normalizedInput.action === "greylist" ? Math.max(1, normalizedInput.allowAfter || 1) : 1,
    minRetryAfter: normalizedInput.action === "greylist" ? normalizedInput.minRetryAfter.trim() : "",
    resetAfter: normalizedInput.action === "greylist" ? normalizedInput.resetAfter.trim() : "",
    code:
      reportAction && normalizedInput.action !== "async-bounce"
        ? 0
        : Math.max(
            normalizedInput.action === "async-bounce" ? 500 : 400,
            Math.min(599, normalizedInput.code || 550)
          ),
    enhancedCode:
      normalizedInput.action === "async-bounce"
        ? normalizedInput.enhancedCode.trim() || "5.0.0"
        : normalizedInput.enhancedCode.trim(),
    feedbackType: normalizedInput.action === "arf" ? normalizedInput.feedbackType || "abuse" : "",
    reportRecipientLocalPart: supportsReportRecipientLocalPart(normalizedInput.action)
      ? normalizedInput.reportRecipientLocalPart.trim()
      : "",
    message: reportAction && normalizedInput.action !== "async-bounce" ? "" : normalizedInput.message.trim()
  };
}

function isReportAction(action: MailFailRule["action"]): boolean {
  return ["arf", "xarf-v3", "xarf-v4", "original-report", "async-bounce"].includes(action);
}

function supportsReportRecipientLocalPart(action: MailFailRule["action"]): boolean {
  return ["arf", "xarf-v3", "xarf-v4"].includes(action);
}

function isValidReportRecipientLocalPart(value: string): boolean {
  return value.length <= 64 && /^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(\.[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*$/.test(value);
}

function mailFailRuleSummary(rule: MailFailRule): string {
  if (rule.action === "async-bounce") {
    return rule.message || "Default delivery failure text.";
  }
  if (isReportAction(rule.action)) {
    return rule.reportRecipientLocalPart
      ? `Generated after acceptance for ${rule.reportRecipientLocalPart}@sender-domain.`
      : "Generated automatically after message acceptance.";
  }
  return rule.message || "No reply message configured.";
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

function SearchIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="7" cy="7" r="4.5" />
      <path d="M10.5 10.5 14 14" />
    </svg>
  );
}

function SettingsIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <circle cx="8" cy="8" r="2.2" />
      <path d="M8 1.8v1.7M8 12.5v1.7M14.2 8h-1.7M3.5 8H1.8M12.8 3.2l-1.2 1.2M4.4 11.6l-1.2 1.2M12.8 12.8l-1.2-1.2M4.4 4.4 3.2 3.2" />
    </svg>
  );
}

function MailIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 4.5h12v7H2z" />
      <path d="M2.5 5 8 9l5.5-4" />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 1.5a6.5 6.5 0 0 0-2 12.7c.3.1.4-.1.4-.3v-1.2c-1.7.4-2.1-.8-2.1-.8-.3-.8-.7-1-1-1.1-.2-.1-.5-.3 0-.3.5 0 1 .5 1.2.7.5.8 1.3.6 1.7.5.1-.4.2-.6.4-.8-1.5-.2-3.1-.8-3.1-3.3 0-.7.2-1.3.7-1.8-.1-.2-.3-.9.1-1.8 0 0 .6-.2 1.9.7a6 6 0 0 1 3.5 0c1.3-.9 1.9-.7 1.9-.7.4.9.2 1.6.1 1.8.4.5.7 1.1.7 1.8 0 2.6-1.6 3.1-3.1 3.3.2.2.5.6.5 1.3v1.9c0 .2.1.4.4.3A6.5 6.5 0 0 0 8 1.5Z" />
    </svg>
  );
}

function ChevronIcon({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 16 16" aria-hidden="true">
      <path d="m4.5 6 3.5 4 3.5-4" />
    </svg>
  );
}

function InfoIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M8 11V7.25M8 5.25h.01M8 14a6 6 0 1 0 0-12 6 6 0 0 0 0 12Z" />
    </svg>
  );
}

function DocsIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4 2.5h5l3 3V13a1 1 0 0 1-1 1H4.8A1.3 1.3 0 0 1 3.5 12.7V3.8A1.3 1.3 0 0 1 4.8 2.5Z" />
      <path d="M9 2.7V6h3.3" />
      <path d="M5.5 8.2h5M5.5 10.2h5M5.5 12.2h3.2" />
    </svg>
  );
}

function HtmlIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="m5.5 4.5-3 3.5 3 3.5M10.5 4.5l3 3.5-3 3.5M8.8 3l-1.6 10" />
    </svg>
  );
}

function TextIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 4h10M5 7.5h6M5 11h6" />
    </svg>
  );
}

function AttachmentIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M10.8 5.1 6.3 9.6a2.4 2.4 0 1 0 3.4 3.4l4-4a4 4 0 1 0-5.6-5.6L3.7 7.8" />
    </svg>
  );
}

function ImageIcon() {
  return (
    <svg className="copyIcon" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2.5" y="3" width="11" height="10" rx="1.5" />
      <circle cx="6" cy="6.2" r="1" />
      <path d="m4 11 2.2-2.2 1.9 1.9 2.2-2.7L12 11" />
    </svg>
  );
}
