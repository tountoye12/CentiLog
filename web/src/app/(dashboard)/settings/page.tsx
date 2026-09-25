"use client";

import { useEffect, useState, type FormEvent } from "react";
import {
  apiRequest,
  errorMessage,
  type ApiService,
  type ApiUser,
} from "@/lib/api";

interface CreateServiceResult {
  service: ApiService;
  api_key: string;
  warning: string;
}

export default function SettingsPage() {
  const [account, setAccount] = useState<ApiUser | null>(null);
  const [services, setServices] = useState<ApiService[]>([]);
  const [retention, setRetention] = useState<Record<string, string>>({});
  const [serviceName, setServiceName] = useState("");
  const [newRetention, setNewRetention] = useState("30");
  const [newAPIKey, setNewAPIKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [userEmail, setUserEmail] = useState("");
  const [userPassword, setUserPassword] = useState("");
  const [userRole, setUserRole] = useState<"viewer" | "admin">("viewer");
  const [userServices, setUserServices] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function loadSettings() {
    const [currentAccount, currentServices] = await Promise.all([
      apiRequest<ApiUser>("/api/me"),
      apiRequest<ApiService[]>("/api/services"),
    ]);
    setAccount(currentAccount);
    setServices(currentServices);
    setRetention(Object.fromEntries(currentServices.map((item) => [item.name, String(item.retention_days)])));
  }

  useEffect(() => {
    let active = true;
    Promise.all([apiRequest<ApiUser>("/api/me"), apiRequest<ApiService[]>("/api/services")])
      .then(([currentAccount, currentServices]) => {
        if (!active) return;
        setAccount(currentAccount);
        setServices(currentServices);
        setRetention(Object.fromEntries(currentServices.map((item) => [item.name, String(item.retention_days)])));
        setError(null);
      })
      .catch((requestError: unknown) => {
        if (active) setError(errorMessage(requestError));
      });
    return () => {
      active = false;
    };
  }, []);

  async function createService(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setNotice(null);
    setNewAPIKey(null);
    try {
      const result = await apiRequest<CreateServiceResult>("/api/services", {
        method: "POST",
        body: JSON.stringify({ name: serviceName.trim(), retention_days: Number(newRetention) }),
      });
      setNewAPIKey(result.api_key);
      setServiceName("");
      setNotice("Service created.");
      await loadSettings();
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setBusy(false);
    }
  }

  async function saveRetention(name: string) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await apiRequest(`/api/services/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify({ retention_days: Number(retention[name]) }),
      });
      setNotice(`Retention updated for ${name}.`);
      await loadSettings();
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setBusy(false);
    }
  }

  async function createUser(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await apiRequest<ApiUser>("/api/users", {
        method: "POST",
        body: JSON.stringify({
          email: userEmail.trim(),
          password: userPassword,
          role: userRole,
          services: userRole === "viewer" ? userServices : [],
        }),
      });
      setNotice(`User ${userEmail.trim()} created.`);
      setUserEmail("");
      setUserPassword("");
      setUserServices([]);
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setBusy(false);
    }
  }

  function toggleService(name: string) {
    setUserServices((current) => current.includes(name)
      ? current.filter((item) => item !== name)
      : [...current, name]);
  }

  async function copyAPIKey() {
    if (!newAPIKey) return;
    try {
      await navigator.clipboard.writeText(newAPIKey);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      setError("Clipboard access was blocked. Select and copy the key manually.");
    }
  }

  if (account && account.role !== "admin") {
    return (
      <section className="page-wrap">
        <div className="alert" role="alert">Administrator access required.</div>
      </section>
    );
  }

  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Administration</p>
          <h1 className="page-title">Settings</h1>
          <p className="page-subtitle">Services, retention, and user access.</p>
        </div>
      </div>
      {error && <div className="alert" role="alert">{error}</div>}
      {notice && <div className="notice" role="status">{notice}</div>}

      {newAPIKey && (
        <section className="key-reveal" aria-live="polite">
          <div>
            <p className="field-label">New service API key</p>
            <code className="mono">{newAPIKey}</code>
          </div>
          <button className="button button-secondary" onClick={() => void copyAPIKey()}>
            {copied ? "Copied" : "Copy key"}
          </button>
          <p className="key-warning">This key is shown once. Store it securely before leaving this page.</p>
        </section>
      )}

      <div className="settings-grid">
        <section className="table-wrap">
          <div className="section-head"><h2 className="section-title">Services</h2><span className="muted">{services.length} total</span></div>
          {services.length === 0 ? (
            <div className="empty-state"><strong>Loading services</strong></div>
          ) : services.map((item) => (
            <div className="service-row" key={item.name}>
              <div>
                <div className="service-name">{item.name}</div>
                <div className="muted">Created {new Date(item.created_at).toLocaleDateString()}</div>
              </div>
              <label className="field">
                <span className="field-label">Retention days</span>
                <input className="input" type="number" min="1" value={retention[item.name] ?? item.retention_days} onChange={(event) => setRetention((current) => ({ ...current, [item.name]: event.target.value }))} />
              </label>
              <button className="button button-secondary" disabled={busy} onClick={() => void saveRetention(item.name)}>
                <Save className="button-icon" />Save
              </button>
            </div>
          ))}
          <form className="form-stack" onSubmit={createService}>
            <h3 className="section-title">Create service</h3>
            <div className="form-row">
              <label className="field">
                <span className="field-label">Service name</span>
                <input className="input" required value={serviceName} onChange={(event) => setServiceName(event.target.value)} placeholder="payments-api" />
              </label>
              <label className="field">
                <span className="field-label">Retention days</span>
                <input className="input" type="number" min="1" required value={newRetention} onChange={(event) => setNewRetention(event.target.value)} />
              </label>
              <button className="button" type="submit" disabled={busy}>Create</button>
            </div>
          </form>
        </section>

        <section className="surface">
          <div className="section-head"><h2 className="section-title">Create user</h2></div>
          <form className="form-stack" onSubmit={createUser}>
            <label className="field">
              <span className="field-label">Email</span>
              <input className="input" type="email" required value={userEmail} onChange={(event) => setUserEmail(event.target.value)} placeholder="viewer@company.com" />
            </label>
            <label className="field">
              <span className="field-label">Temporary password</span>
              <input className="input" type="password" required minLength={8} value={userPassword} onChange={(event) => setUserPassword(event.target.value)} autoComplete="new-password" />
            </label>
            <label className="field">
              <span className="field-label">Role</span>
              <select className="select" value={userRole} onChange={(event) => setUserRole(event.target.value as "admin" | "viewer")}>
                <option value="viewer">Viewer</option>
                <option value="admin">Admin</option>
              </select>
            </label>
            {userRole === "viewer" && (
              <div className="field">
                <span className="field-label">Allowed services</span>
                <div className="permission-list">
                  {services.map((item) => (
                    <label className="permission-option" key={item.name}>
                      <input type="checkbox" checked={userServices.includes(item.name)} onChange={() => toggleService(item.name)} />
                      {item.name}
                    </label>
                  ))}
                  {services.length === 0 && <span className="muted">No services available</span>}
                </div>
              </div>
            )}
            <button className="button" type="submit" disabled={busy}>Create user</button>
          </form>
        </section>
      </div>
    </>
  );
}