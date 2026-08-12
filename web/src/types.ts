export type Attachment = {
  id: number;
  messageId: number;
  fileName: string;
  contentType: string;
  contentId: string;
  size: number;
  inline: boolean;
};

export type Header = {
  key: string;
  value: string;
};

export type Message = {
  id: number;
  receivedAt: string;
  mailFrom: string;
  rcptTo: string[];
  tags: string[];
  headerFrom: string;
  headerTo: string;
  subject: string;
  messageId: string;
  helo: string;
  remoteIp: string;
  size: number;
  raw?: string;
  textBody?: string;
  htmlBody?: string;
  headers?: Header[];
  attachments?: Attachment[];
};

export type Stats = {
  messageCount: number;
  totalSize: number;
  latestReceivedAt?: string;
};

export type AppInfo = {
  version: string;
};

export type MailFailRule = {
  name: string;
  trigger: string;
  stage: "mailfrom" | "rcpt" | "data";
  action: "reject" | "greylist" | "arf" | "xarf-v3" | "xarf-v4" | "original-report" | "async-bounce";
  allowAfter: number;
  minRetryAfter: string;
  resetAfter: string;
  code: number;
  enhancedCode: string;
  feedbackType: "" | "abuse" | "fraud" | "virus" | "other" | "not-spam";
  message: string;
};

export type MessagePage = {
  messages: Message[];
  availableTags: string[];
  nextCursor?: string;
  hasMore: boolean;
};

export type AppSettings = {
  allowedOrigins: string;
  smtpLogVerbose: boolean;
  mailFailEnabled: boolean;
  mailFailRules: MailFailRule[];
  reportFrom: string;
  allowedRemoteIps: string;
  acceptedRcptDomains: string;
  acceptedFromDomains: string;
  autoDeleteAfterDays: number;
};

export type SessionInfo = {
  username: string;
  isAdmin: boolean;
  userId?: number;
};

export type User = {
  id: number;
  username: string;
  settings: AppSettings;
  createdAt?: string;
  updatedAt?: string;
};
