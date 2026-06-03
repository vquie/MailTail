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

export type MessagePage = {
  messages: Message[];
  nextCursor?: string;
  hasMore: boolean;
};

export type AppSettings = {
  allowedOrigins: string;
  smtpLogVerbose: boolean;
  mailFailEnabled: boolean;
  mailFailRulesFile: string;
  allowedRemoteIps: string;
  acceptedRcptDomains: string;
  acceptedFromDomains: string;
};
