# Learnings - 2026-06-28 17:38

- In internal/runtime/loop/loop.go, the cache-break check (checkCacheHealth) runs during streaming via the 'usage' ChatDelta, which is AFTER the request was built. To diff request components on break, hash them in runLoop right after building req and stash on the Loop (curr* fields), then promote curr*->last* inside checkCacheHealth each call.
- types.ChatContentBlock/SystemBlock have CacheControl with json omitempty, so stripping CacheControl before hashing makes JSON byte-identical regardless of whether the breakpoint sat on that block. This is the key to avoiding false-positive cache-break diffs since addMessageCacheControl moves the breakpoint every call.
