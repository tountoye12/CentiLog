"use client";

import { useEffect, useState } from "react";
import { apiRequest, errorMessage, type PatternRecord } from "@/lib/api";

const hourOptions = [6, 24, 72];

export default function NoisePage() {
  const [hours, setHours] = useState(24);
  const [patterns, setPatterns] = useState<PatternRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(true);

  useEffect(() => {
    let active = true;
    setBusy(true);
    apiRequest<PatternRecord[]>(`/api/stats/patterns?hours=${hours}`)
      .then((result) => {
        if (active) {
          setPatterns(result);
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
  }, [hours]);

  return (
    <>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Signal quality</p>
          <h1 className="page-title">Noise patterns</h1>
          <p className="page-subtitle">Repeated message shapes, ranked by volume.</p>
        </div>
        <div className="range-control" aria-label="Pattern time range">
          {hourOptions.map((option) => (
            <button key={option} className={`range-button${hours === option ? " range-button-active" : ""}`} onClick={() => setHours(option)}>
              {option}h
            </button>
          ))}
        </div>
      </div>
      {error && <div className="alert" role="alert">{error}</div>}
      <section className="table-wrap">
        <div className="section-head">
          <h2 className="section-title">Top patterns</h2>
          <span className="muted">{busy ? "Updating" : `${patterns.length} patterns`}</span>
        </div>
        {patterns.length === 0 ? (
          <div className="empty-state"><strong>{busy ? "Loading patterns" : "No patterns found"}</strong></div>
        ) : (
          <table className="data-table">
            <thead><tr><th>Pattern</th><th>Count</th><th>Last seen</th></tr></thead>
            <tbody>{patterns.map((item, index) => (
              <tr key={`${item.pattern}-${index}`}>
                <td className="message-cell mono">{item.pattern}</td>
                <td>{Number(item.log_count).toLocaleString()}</td>
                <td>{new Date(item.last_seen).toLocaleString()}</td>
              </tr>
            ))}</tbody>
          </table>
        )}
      </section>
      {busy && <div className="loading-inline">Updating</div>}
    </>
  );
}