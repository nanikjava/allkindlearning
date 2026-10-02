-- Lesson 02 / experiment 2: data types and compression codecs.

DROP TABLE IF EXISTS shop.ev_codecs;

CREATE TABLE shop.ev_codecs
(
    -- same column stored three ways so we can compare them side by side
    ts_default   DateTime64(3, 'UTC'),
    ts_delta     DateTime64(3, 'UTC') CODEC(Delta, ZSTD(3)),     -- sorted timestamps: store the gaps
    ts_dd        DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(3)),

    country_str  String,
    country_lc   LowCardinality(String),
    country_enum Enum8('US'=1,'DE'=2,'IN'=3,'BR'=4,'GB'=5,'FR'=6,'JP'=7,'ID'=8,'NG'=9,'CA'=10),

    dur_u32      UInt32,
    dur_u16      UInt16,                                          -- 30000 fits: pick the smallest type that fits
    dur_t64      UInt32 CODEC(T64, ZSTD(1))
)
ENGINE = MergeTree
ORDER BY ts_default;

INSERT INTO shop.ev_codecs
SELECT event_time, event_time, event_time,
       country, country, country,
       duration_ms, duration_ms, duration_ms
FROM shop.events;

OPTIMIZE TABLE shop.ev_codecs FINAL;

SELECT name, type, compression_codec AS codec,
       formatReadableSize(data_compressed_bytes) AS on_disk,
       round(data_uncompressed_bytes / data_compressed_bytes, 1) AS ratio
FROM system.columns
WHERE database = 'shop' AND table = 'ev_codecs'
ORDER BY name;
