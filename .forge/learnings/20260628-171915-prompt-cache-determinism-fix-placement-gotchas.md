# Learnings - 2026-06-28 17:19

- internal/runtime/prompt/prompt_test.go asserts dynamic-block content via fixed prefix slices like block.Text[0:17]=="<system-reminder>" and =="Available Skills:". Prepending anything (e.g. a date line) to the dynamic block breaks these prefix checks — append trailing content instead of prepending.
- Go's os.ReadDir returns entries already sorted by filename (guaranteed since Go 1.16), so loader-level enumeration is deterministic per-directory. Issue #240 overstated ReadDir non-determinism; the real fix is sorting in prompt.Assemble (defense-in-depth + cross-level merge), not touching loader.go.
- forge prompt cache strategy: block 0 is scope:'global' (shared cross-session), block 1 is per-session dynamic. Never put daily-changing data (like the current date) in block 0 — it busts the large shared cache every midnight. Put it at the END of block 1 to keep that block's prefix byte-stable within a day.
