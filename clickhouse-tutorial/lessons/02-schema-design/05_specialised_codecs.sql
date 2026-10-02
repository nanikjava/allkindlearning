-- Lesson 02 / extra: each specialised codec on 10M rows of data it is designed for.
-- Run: ch --queries-file /lessons/02-schema-design/05_specialised_codecs.sql
DROP TABLE IF EXISTS default.codec_demo;
CREATE TABLE default.codec_demo (
  ts_lz4        DateTime64(3) CODEC(LZ4),
  ts_delta      DateTime64(3) CODEC(Delta, ZSTD(1)),
  ts_dd         DateTime64(3) CODEC(DoubleDelta, ZSTD(1)),
  temp_lz4      Float64 CODEC(LZ4),
  temp_gorilla  Float64 CODEC(Gorilla, ZSTD(1)),
  temp_fpc      Float64 CODEC(FPC, ZSTD(1)),
  dur_lz4       UInt32 CODEC(LZ4),
  dur_t64       UInt32 CODEC(T64, ZSTD(1)),
  cents_lz4     UInt64 CODEC(LZ4),
  cents_gcd     UInt64 CODEC(GCD, ZSTD(1)),
  cents_zstd    UInt64 CODEC(ZSTD(1))
) ENGINE = MergeTree ORDER BY tuple();
INSERT INTO default.codec_demo SELECT
  toDateTime64('2026-07-01 00:00:00',3) + toIntervalMillisecond(number*10000) AS t,
  t, t,
  20 + 5*sin(number/5000) + (cityHash64(number)%10)/100 AS temp, temp, temp,
  toUInt32(cityHash64(number,1)%30000) AS d, d,
  (cityHash64(number,2)%10000)*500 AS c, c, c
FROM numbers(10000000);
OPTIMIZE TABLE default.codec_demo FINAL;
SELECT name, compression_codec AS codec, formatReadableSize(data_uncompressed_bytes) AS raw, formatReadableSize(data_compressed_bytes) AS on_disk, round(data_uncompressed_bytes/data_compressed_bytes,1) AS ratio
FROM system.columns WHERE table='codec_demo' AND database='default' FORMAT PrettyCompactMonoBlock;
