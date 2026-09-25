"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, type FormEvent } from "react";
import { apiRequest, errorMessage, getToken, storeToken, type LoginResult } from "@/lib/api";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (getToken()) router.replace("/");
  }, [router]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const result = await apiRequest<LoginResult>("/api/login", {
        method: "POST",
        authenticated: false,
        redirectOnUnauthorized: false,
        body: JSON.stringify({ email, password }),
      });
      storeToken(result.access_token);
      router.replace("/");
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="login-screen">
      <div className="login-frame">
        <div className="brand login-brand">
          <span className="brand-mark" aria-hidden="true">C</span>
          <span className="brand-name">Centilog</span>
        </div>
        <section className="login-panel">
          <p className="eyebrow">Log operations</p>
          <h1>Sign in</h1>
          <p>Access your services and log streams.</p>
          {error && <div className="alert" role="alert">{error}</div>}
          <form className="login-form" onSubmit={submit}>
            <label className="field">
              <span className="field-label">Email</span>
              <input className="input" type="email" autoComplete="username" required value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@company.com" />
            </label>
            <label className="field">
              <span className="field-label">Password</span>
              <input className="input" type="password" autoComplete="current-password" required value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Enter your password" />
            </label>
            <button className="button" type="submit" disabled={busy}>
              {busy ? "Signing in" : "Sign in"}
            </button>
          </form>
        </section>
        <div className="login-foot">CENTILOG · SECURE ACCESS</div>
      </div>
    </main>
  );
}