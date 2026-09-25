"use client";

import { useEffect, useState } from "react";
import { apiRequest, errorMessage, type VolumeRecord } from "@/lib/api";

const dayOptions = [7, 30, 90];
const numberFormat = new Intl.NumberFormat();

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${numberFormat.format(bytes)} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(value >= 100 ? 0 : 1)} ${units[unit]}`;
}

export default function CostPage() {
  const [days, setDays] = useState(30);
  const [volume, setVolume] = useState<VolumeRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(true);

  useEffect(() => {
    let active = true;
    setBusy(true);
    apiRequest<VolumeRecord[]>(`/api/stats/volume?days=${days}`)
      .then((result) => {
        if (active) {
          setVolume(result);
          setError(null);
        }
      })
      .catch((requestError: unknown) => {
        if (active) setError(errorMessage(requestError));
      })
      .finally(() => {
        if (active) setBusy(false);
      });
    return () => {
      active = false;
    };
  }, [days]);

  const totalLogs = volume.reduce((total, item) => total + Number(item.log_count), 0);
  const totalBytes = volume.reduce((total, item) => total + Number(item.raw_bytes), 0);
  const services = new Set(volume.map((item) => item.service)).size;

  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Ingest volume</p>
          <h1 className="page-title">Volume by service</h1>
          <p className="page-subtitle">Daily event counts and uncompressed record bytes.</p>
        </div>
        <div className="range-control" aria-label="Volume time range">
          {dayOptions.map((option) => (
            <button key={option} className={`range-button${days === option ? " range-button-active" : ""}`} onClick={() => setDays(option)}>
              {option}d
            </button>
          ))}
        </div>
      </div>
      {error && <div className="alert" role="alert">{error}</div>}
      <section className="surface stats-summary">
        <div><span className="muted">Events</span><strong>{numberFormat.format(totalLogs)}</strong></div>
        <div><span className="muted">Raw bytes</span><strong>{formatBytes(totalBytes)}</strong></div>
        <div><span className="muted">Services</span><strong>{numberFormat.format(services)}</strong></div>
        <span className="summary-state">{busy ? "Updating" : "Current range"}</span>
      </section>
      <section className="table-wrap">
        <div className="section-head"><h2 className="section-title">Daily volume</h2></div>
        {volume.length === 0 ? (
          <div className="empty-state"><strong>{busy ? "Loading volume" : "No volume found"}</strong></div>
        ) : (
          <table className="data-table">
            <thead><tr><th>Day (UTC)</th><th>Service</th><th>Log count</th><th>Raw bytes</th></tr></thead>
            <tbody>{volume.map((item, index) => (
              <tr key={`${item.day}-${item.service}-${index}`}>
                <td className="mono">{item.day}</td>
                <td>{item.service}</td>
                <td>{numberFormat.format(Number(item.log_count))}</td>
                <td>{formatBytes(Number(item.raw_bytes))}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </section>
    </>
  );
}