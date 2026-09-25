CREATE DATABASE IF NOT EXISTS centilog;

CREATE TABLE IF NOT EXISTS centilog.logs
(
    timestamp DateTime64(3, 'UTC'),
    service LowCardinality(String),
    host LowCardinality(String),
    env LowCardinality(String),
    level LowCardinality(String),
    message String,
    trace_id String DEFAULT '',
    attributes Map(String, String) DEFAULT map(),
    redacted Bool DEFAULT false,
    ingested_at DateTime64(3, 'UTC') DEFAULT now64(3, 'UTC'),
    retention_days UInt16,
    INDEX trace_id_bloom trace_id TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX message_token message TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toDate(timestamp)
ORDER BY (service, level, timestamp)
TTL timestamp + toIntervalDay(retention_days) DELETE
SETTINGS index_granularity = 8192;
