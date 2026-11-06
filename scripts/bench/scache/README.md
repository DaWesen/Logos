# scache local benchmark

This harness measures the in-process exact and vector lookup paths only:

- no Redis
- no embedding API
- one scope with deterministic synthetic vectors
- judge disabled

It is a regression check, not a production load test. Do not copy its numbers
into a resume or PR as online P99/QPS.

```bash
go run ./scripts/bench/scache
go run ./scripts/bench/scache -entries 200 -lookups 50000 -dim 256
```

The output includes p50/p95/p99, max, mean and a single-threaded QPS estimate,
plus the local index entry count and estimated bytes. Run it before and after
changing the index, slot gate or lookup order; compare on the same machine.

Hit logging is disabled so the numbers describe the lookup path rather than
stdout. Each production hit still writes one `scache_hit` Info log, so do not
read the QPS here as a production capacity number. Values below one microsecond
print as `<1µs` because the Windows timer cannot resolve them reliably.

Real load testing still needs the scenarios in
`docs/semantic-cache-v2-spec.md` §15.4 with production-like Redis and embedding
latency. Those numbers are currently marked "未执行".
