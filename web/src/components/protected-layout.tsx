"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";
import { AppShell } from "@/components/app-shell";
import { apiRequest, errorMessage, getToken, type ApiUser } from "@/lib/api";

export function ProtectedLayout({ children }: { children: ReactNode }) {
  const router = useRouter();
  const [account, setAccount] = useState<ApiUser | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    if (!getToken()) {
      router.replace("/login");
      return;
    }

    let active = true;
    const controller = new AbortController();
    apiRequest<ApiUser>("/api/me", { signal: controller.signal })
      .then((currentAccount) => {
        if (active) setAccount(currentAccount);
      })
      .catch((requestError: unknown) => {
        if (active && !(requestError instanceof DOMException && requestError.name === "AbortError")) {
          setError(errorMessage(requestError));
        }
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [retry, router]);

  if (account) return <AppShell account={account}>{children}</AppShell>;

  if (error) {
    return (
      <main className="loading-screen">
        <div className="login-frame">
          <div className="alert" role="alert">{error}</div>
          <button className="button button-secondary" onClick={() => setRetry((value) => value + 1)}>
            Retry session check
          </button>
        </div>
      </main>
    );
  }

  return (
    <main className="loading-screen" aria-label="Checking session">
      <span className="spinner" />
      <span>Checking session</span>
    </main>
  );
}