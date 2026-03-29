# Queue System Visual Flow

## Component Diagram

```
┌─────────────┐
│    User     │
└──────┬──────┘
       │
       │ "Add feature and test after each change"
       ▼
┌─────────────────────────────────────────┐
│           Agent Worker                   │
│                                          │
│  ┌────────────────────────────────┐     │
│  │      Conversation Loop         │     │
│  │                                │     │
│  │  Claude sees user message      │     │
│  │  ↓                             │     │
│  │  Claude: QueueImmediate(       │     │
│  │           "go test")           │     │
│  │  ↓                             │     │
│  │  Emits: queue_immediate event  │     │
│  └────────────┬───────────────────┘     │
│               │                          │
│  ┌────────────▼───────────────────┐     │
│  │    emit() function             │     │
│  │                                │     │
│  │  Intercepts: queue_immediate   │     │
│  │  → Hub.EnqueueImmediate()      │     │
│  │                                │     │
│  │  Intercepts: tool_use          │     │
│  │  → executeImmediateQueue()     │     │
│  │     → Bash("go test")          │     │
│  │     → Emit queued_task_result  │     │
│  │                                │     │
│  │  Intercepts: done              │     │
│  │  → executeCompletionQueue()    │     │
│  │     → Run completion commands   │     │
│  └────────────────────────────────┘     │
│                                          │
│  ┌────────────────────────────────┐     │
│  │           Hub                  │     │
│  │                                │     │
│  │  immediateQueue: []string      │     │
│  │  completionQueue: []string     │     │
│  └────────────────────────────────┘     │
└─────────────────────────────────────────┘
```

## Sequence Diagram: User Asks to Test After Each Change

```
User             Worker           Loop            Hub            Tools
 │                 │                │               │              │
 │ "test after    │                │               │              │
 │  each change"  │                │               │              │
 ├───────────────►│                │               │              │
 │                │ Send(prompt)   │               │              │
 │                ├───────────────►│               │              │
 │                │                │ Chat API      │              │
 │                │                ├──────────────►│              │
 │                │                │               │              │
 │                │                │ tool_use:     │              │
 │                │                │ QueueImmediate│              │
 │                │◄───────────────┤               │              │
 │                │ emit(event)    │               │              │
 │                │ type="queue_   │               │              │
 │                │  immediate"    │               │              │
 │                │                │               │              │
 │                │ EnqueueImmediate("go test")    │              │
 │                ├───────────────────────────────►│              │
 │                │                │               │              │
 │                │                │ tool_use:     │              │
 │                │                │ Write(file)   │              │
 │                │◄───────────────┤               │              │
 │                │ emit(event)    │               │              │
 │                │ type="tool_use"│               │              │
 │                │                │               │              │
 │                │ executeImmediateQueue()        │              │
 │                │                │               │              │
 │                │ GetImmediateQueue()            │              │
 │                ├───────────────────────────────►│              │
 │                │                │          ["go test"]         │
 │                │◄───────────────────────────────┤              │
 │                │                │               │              │
 │                │ Execute("Bash", {"command": "go test"})       │
 │                ├──────────────────────────────────────────────►│
 │                │                │               │     runs     │
 │                │◄──────────────────────────────────────────────┤
 │                │                │               │     result   │
 │                │                │               │              │
 │                │ emit(queued_task_result)       │              │
 │◄───────────────┤                │               │              │
 │ "Tests passed" │                │               │              │
 │                │                │               │              │
 │                │                │ done          │              │
 │                │◄───────────────┤               │              │
 │◄───────────────┤                │               │              │
 │ "Done"         │                │               │              │
```

## Sequence Diagram: User Asks to Commit When Done

```
User             Worker           Loop            Hub            Tools
 │                 │                │               │              │
 │ "commit when   │                │               │              │
 │  done"         │                │               │              │
 ├───────────────►│                │               │              │
 │                │ Send(prompt)   │               │              │
 │                ├───────────────►│               │              │
 │                │                │               │              │
 │                │                │ tool_use:     │              │
 │                │                │ QueueOnComplete              │
 │                │◄───────────────┤               │              │
 │                │ emit(event)    │               │              │
 │                │ type="queue_   │               │              │
 │                │  on_complete"  │               │              │
 │                │                │               │              │
 │                │ EnqueueCompletion("git commit")│              │
 │                ├───────────────────────────────►│              │
 │                │                │               │              │
 │                │                │ tool_use:     │              │
 │                │                │ Edit(file1)   │              │
 │                │◄───────────────┤               │              │
 │                │                │               │              │
 │                │                │ tool_use:     │              │
 │                │                │ Edit(file2)   │              │
 │                │◄───────────────┤               │              │
 │                │                │               │              │
 │                │                │ done          │              │
 │                │◄───────────────┤               │              │
 │                │ emit(event)    │               │              │
 │                │ type="done"    │               │              │
 │                │                │               │              │
 │                │ executeCompletionQueue()       │              │
 │                │                │               │              │
 │                │ PullCompletionQueue()          │              │
 │                ├───────────────────────────────►│              │
 │                │                │     ["git commit"]           │
 │                │◄───────────────────────────────┤              │
 │                │                │               │ (clears)     │
 │                │                │               │              │
 │                │ Execute("Bash", {"command": "git commit"})    │
 │                ├──────────────────────────────────────────────►│
 │                │                │               │     runs     │
 │                │◄──────────────────────────────────────────────┤
 │                │                │               │    result    │
 │                │                │               │              │
 │                │ emit(queued_task_result)       │              │
 │◄───────────────┤                │               │              │
 │ "Committed"    │                │               │              │
 │                │                │               │              │
 │                │ emit(done)     │               │              │
 │◄───────────────┤                │               │              │
 │ "Done"         │                │               │              │
```

## State Machine: Immediate Queue

```
┌─────────────┐
│   Empty     │
└──────┬──────┘
       │
       │ QueueImmediate("cmd")
       │ emit(queue_immediate)
       ▼
┌─────────────┐
│   Queued    │◄────┐
│  ["cmd"]    │     │
└──────┬──────┘     │
       │            │ Any tool_use event
       │ tool_use   │ triggers execution
       │ event      │ but queue persists
       ▼            │
┌─────────────┐     │
│  Execute    │     │
│   "cmd"     │     │
└──────┬──────┘     │
       │            │
       └────────────┘
       
       │ QueueImmediate("")
       ▼
┌─────────────┐
│  Cleared    │
│     []      │
└─────────────┘
```

## State Machine: Completion Queue

```
┌─────────────┐
│   Empty     │
└──────┬──────┘
       │
       │ QueueOnComplete("cmd")
       │ emit(queue_on_complete)
       ▼
┌─────────────┐
│   Queued    │
│  ["cmd"]    │
└──────┬──────┘
       │
       │ done event
       ▼
┌─────────────┐
│  Execute    │
│   "cmd"     │
└──────┬──────┘
       │
       │ (auto-clears)
       ▼
┌─────────────┐
│   Empty     │
└─────────────┘
```

## Data Flow: Events

```
┌──────────────────────────────────────────┐
│           Event Stream                    │
├──────────────────────────────────────────┤
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ text: "I'll create the feature..."  │  │
│ └─────────────────────────────────────┘  │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ tool_use: QueueImmediate           │  │
│ └─────────────────────────────────────┘  │
│           ▼                               │
│      Hub.EnqueueImmediate()               │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ queue_immediate: "go test"         │  │
│ └─────────────────────────────────────┘  │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ tool_use: Write                    │  │
│ └─────────────────────────────────────┘  │
│           ▼                               │
│      Worker.executeImmediateQueue()       │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ queued_task_result:                │  │
│ │ "[immediate] go test\nPASS"        │  │
│ └─────────────────────────────────────┘  │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ tool_use: Edit                     │  │
│ └─────────────────────────────────────┘  │
│           ▼                               │
│      Worker.executeImmediateQueue()       │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ queued_task_result:                │  │
│ │ "[immediate] go test\nPASS"        │  │
│ └─────────────────────────────────────┘  │
│                                           │
│ ┌─────────────────────────────────────┐  │
│ │ done                               │  │
│ └─────────────────────────────────────┘  │
│                                           │
└──────────────────────────────────────────┘
```
