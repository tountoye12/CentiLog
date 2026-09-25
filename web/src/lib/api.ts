export const tokenStorageKey = "centilog.jwt";

export interface ApiUser {
  id: number;
  email: string;
  role: "admin" | "viewer";
}

export interface ApiService {
  name: string;
  retention_days: number;
  created_at: string;
}

export interface LogRecord {
  timestamp: string;
  service: string;
  host: string;
  env: string;
  level: string;
  message: string;
  trace_id: string;
  attributes: Record<string, string>;
  redacted: boolean;
  ingested_at: string;
  retention_days: number;
}

export interface VolumeRecord {
  day: string;
  service: string;
  log_count: number;
  raw_bytes: number;
}

export interface PatternRecord {
  pattern: string;
  log_count: number;
  last_seen: string;
}

export interface LoginResult {
  access_token: string;
  token_type: "Bearer";
  expires_at: string;
}

export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

interface ApiRequestOptions extends RequestInit {
  authenticated?: boolean;
  redirectOnUnauthorized?: boolean;
}

export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(tokenStorageKey);
}

export function storeToken(token: string): void {
  window.localStorage.setItem(tokenStorageKey, token);
}

export function clearToken(): void {
  if (typeof window !== "undefined") {
    window.localStorage.removeItem(tokenStorageKey);
  }
}

export async function apiRequest<T>(
  path: string,
  options: ApiRequestOptions = {},
): Promise<T> {
  const {
    authenticated = true,
    redirectOnUnauthorized = true,
    headers: suppliedHeaders,
    ...requestOptions
  } = options;
  const headers = new Headers(suppliedHeaders);

  if (requestOptions.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  if (authenticated) {
    const token = getToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
  }

  const response = await fetch(path, {
    ...requestOptions,
    headers,
    cache: "no-store",
  });
  const responseText = await response.text();
  let payload: unknown;

  if (responseText) {
    try {
      payload = JSON.parse(responseText) as unknown;
    } catch {
      payload = responseText;
    }
  }

  if (!response.ok) {
    if (response.status === 401 && redirectOnUnauthorized && typeof window !== "undefined") {
      clearToken();
      if (window.location.pathname !== "/login") {
        window.location.replace("/login");
      }
    }

    const message =
      typeof payload === "object" && payload !== null && "error" in payload &&
      typeof payload.error === "string"
        ? payload.error
        : typeof payload === "object" && payload !== null && "message" in payload &&
            typeof payload.message === "string"
          ? payload.message
          : typeof payload === "string" && payload.length > 0
            ? payload
            : `Request failed (${response.status})`;
    throw new ApiError(message, response.status);
  }

  return payload as T;
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Request failed. Please try again.";
}