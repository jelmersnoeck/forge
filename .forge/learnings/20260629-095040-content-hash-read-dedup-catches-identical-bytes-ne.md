# Learnings - 2026-06-29 09:50

- internal/tools/read.go already deduped re-reads via mtime + exact offset/limit match, returning FileUnchangedStub. The gap (issue #253) was newer-mtime-but-identical-bytes (touch/no-op-save/git-checkout). Fix is a content-hash fallback hashing ONLY the windowed bytes returned (not the whole file), so the hash matches dedup granularity.
- Adding content-hash dedup broke a pre-existing test ('bash-modified file detected via mtime' in read_dedup_test.go) that simulated external edits by rewinding the stored mtime while leaving content identical — the new hash logic correctly dedupes that. When tightening dedup, audit tests whose premise was 'mtime-mismatch alone forces fresh read'; they must change content too.
