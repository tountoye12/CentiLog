"use client";

import { useEffect, useState, type FormEvent } from "react";
import { apiRequest, errorMessage, type ApiService, type LogRecord } from "@/lib/api";

function asMilliseconds(value: string): string {
  if (!value) return "";
  const milliseconds = new Date(value).getTime();
  return Number.isFinite(milliseconds) ? String(milliseconds) : "";
}

function formatTimestamp(value: string): string {
  const timestamp = new Date(value);
  return Number.isNaN(timestamp.getTime()) ? value : timestamp.toLocaleString();
}

export default function SearchPage() {
  const [services, setServices] = useState<ApiService[]>([]);
  const [logs, setLogs] = useState<LogRecord[]>([]);
  const [service, setService] = useState("");
  const [level, setLevel] = useState("");
  const [text, setText] = useState("");
  const [traceID, setTraceID] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let active = true;
    apiRequest<ApiService[]>("/api/services")
      .then((result) => {
        if (active) setServices(result);
      })
      .catch((requestError: unknown) => {
        if (active) setError(errorMessage(requestError));
      });
    apiRequest<LogRecord[]>("/api/logs?limit=100")
      .then((result) => {
        if (active) {
          setLogs(result);
          setLoaded(true);
        }
      })
      .catch((requestError: unknown) => {
        if (active) {
          setError(errorMessage(requestError));
          setLoaded(true);
        }
      });
    return () => {
      active = false;
    };
  }, []);

  async function search(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    setBusy(true);
    setError(null);
    const parameters = new URLSearchParams({ limit: "100" });
    if (service) parameters.set("service", service);
    if (level) parameters.set("level", level);
    if (text.trim()) parameters.set("text", text.trim());
    if (traceID.trim()) parameters.set("trace_id", traceID.trim());
    if (from) parameters.set("from", asMilliseconds(from));
    if (to) parameters.set("to", asMilliseconds(to));

    try {
      setLogs(await apiRequest<LogRecord[]>(`/api/logs?${parameters.toString()}`));
      setLoaded(true);
    } catch (requestError) {
      setError(errorMessage(requestError));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Explore</p>
          <h1 className="page-title">Log search</h1>
          <p className="page-subtitle">Search normalized events across your permitted services.</p>
        </div>
        <button className="button button-secondary" onClick={() => void search()} disabled={busy}>
          Refresh
        </button>
      </div>

      {error && <div className="alert" role="alert">{error}</div>}

      <form className="toolbar" onSubmit={search}>
        <label className="field">
          <span className="field-label">Service</span>
          <select className="select" value={service} onChange={(event) => setService(event.target.value)}>
            <option value="">All permitted</option>
            {services.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}
          </select>
        </label>
        <label className="field">
          <span className="field-label">Level</span>
          <select className="select" value={level} onChange={(event) => setLevel(event.target.value)}>
            <option value="">All levels</option>
            {(["trace", "debug", "info", "warn", "error", "fatal"] as const).map((item) => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <label className="field field-wide">
          <span className="field-label">Text</span>
          <input className="input" value={text} onChange={(event) => setText(event.target.value)} placeholder="Search message" />
        </label>
        <label className="field">
          <span className="field-label">Trace ID</span>
          <input className="input mono" value={traceID} onChange={(event) => setTraceID(event.target.value)} placeholder="trace_id" />
        </label>
        <label className="field">
          <span className="field-label">From</span>
          <input className="input" type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
        </label>
        <label className="field">
          <span className="field-label">To</span>
          <input className="input" type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
        </label>
        <button className="button" type="submit" disabled={busy}>
          {busy ? "Searching" : "Search"}
        </button>
      </form>

      <section className="table-wrap" aria-label="Log results">
        <div className="stats-strip">
          <span>Results<strong>{logs.length}</strong></span>
          <span>Order<strong>Newest first</strong></span>
        </div>
        {logs.length === 0 ? (
          <div className="empty-state">
            <strong>{loaded ? "No matching logs" : "Loading logs"}</strong>
            <span>{loaded ? "Adjust the filters and search again." : ""}</span>
          </div>
        ) : (
          <table className="data-table">
            <thead><tr><th>Timestamp</th><th>Service</th><th>Level</th><th>Host</th><th>Message</th><th>Trace</th></tr></thead>
            <tbody>
              {logs.map((log, index) => (
                <tr key={`${log.trace_id}-${log.timestamp}-${index}`}>
                  <td className="mono">{formatTimestamp(log.timestamp)}</td>
                  <td>{log.service}</td>
                  <td><span className={`level level-${log.level}`}>{log.level}</span></td>
                  <td className="mono">{log.host}</td>
                  <td className="message-cell">{log.message}</td>
                  <td className="mono">{log.trace_id || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </>
  );
}