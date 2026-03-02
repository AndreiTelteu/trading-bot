# LLM Improvement Proposal Process - Implementation Analysis & Plan

## Executive Summary

The AI LLM improvement proposal process has a **solid foundation** with core functionality implemented, but has **several gaps** that prevent it from being fully production-ready. This document outlines what's complete, what's missing, and a prioritized plan to finish the implementation.

---

## Current Implementation Status

### ✅ Complete Components

#### 1. Backend Service Layer (`internal/services/ai.go`)
- `GenerateProposals()` - Orchestrates the proposal generation flow
- `buildProposalPrompt()` - Constructs detailed prompts with market analysis, wallet, positions, and settings
- `callLLM()` - Makes HTTP calls to LLM provider (OpenRouter-compatible)
- `parseProposalsFromResponse()` - Extracts JSON proposals from LLM response
- `ApproveProposal()` - Handles approval logic with setting updates
- `DenyProposal()` - Handles denial logic
- `GetAllProposals()` - Retrieves proposals from database

#### 2. HTTP API Handlers (`internal/handlers/ai.go`)
All four endpoints implemented:
- `GET /api/ai/proposals` - List proposals
- `POST /api/ai/generate-proposals` - Trigger generation
- `POST /api/ai/proposals/:id/approve` - Approve proposal
- `POST /api/ai/proposals/:id/deny` - Deny proposal

#### 3. Database Schema (`internal/database/models.go`)
- `AIProposal` model with all required fields (type, params, status, timestamps, reasoning)
- `LLMConfig` model for provider settings
- Proper indexes and foreign key support

#### 4. Frontend UI (`frontend/src/components/AIProposal.jsx`)
- Proposal list with status filtering (all/pending/approved/denied)
- Generate proposals button with loading state
- Approve/Deny action buttons for pending proposals
- Display of parameter changes and reasoning

#### 5. Route Registration (`cmd/server/main.go`)
All AI routes properly registered under `/api/ai`

---

### ❌ Missing/Incomplete Components

#### 1. **CRITICAL: Automatic Proposal Generation Scheduling**
**File:** `internal/cron/scheduler.go`

The scheduler has placeholder code but the job is never actually scheduled:
```go
var proposalJobID cron.EntryID  // declared but unused

func runGenerateProposals() error { ... }  // defined but never called
func getProposalInterval() string { ... }  // defined but never called
```

The `Start()` function does NOT schedule the proposal generation job.

**Impact:** Users must manually click "Generate Proposals" - no automated analysis.

---

#### 2. **CRITICAL: LLM Configuration API Endpoints**
**File:** Frontend expects endpoints that don't exist

The `LLMConfig.jsx` component calls:
- `GET /api/llm/config` - ❌ Does not exist
- `PUT /api/llm/config` - ❌ Does not exist  
- `POST /api/llm/test` - ❌ Does not exist

**Impact:** Users cannot configure LLM settings through the UI.

---

#### 3. **HIGH: WebSocket Real-Time Notifications**
**File:** `internal/websocket/broadcaster.go`

No WebSocket broadcast functions exist for:
- New proposals generated
- Proposal approved/denied
- Proposal status changes

**Impact:** Frontend must poll for updates; no real-time UX.

---

#### 4. **HIGH: Incomplete Proposal Type Handling**
**File:** `internal/services/ai.go:ApproveProposal()`

Current logic only handles `parameter_adjustment`:
```go
if proposal.ProposalType == "parameter_adjustment" && ... {
    // update setting
}
```

Missing implementation for:
- `buy_signal` - Should trigger position open
- `sell_signal` - Should trigger position close
- `risk_management` - No defined behavior

**Impact:** Buy/sell proposals require manual execution; not truly automated.

---

#### 5. **MEDIUM: No Activity Log Integration**
**Files:** `internal/services/ai.go`, `internal/handlers/activity.go`

AI proposal actions (generate, approve, deny, apply) are not logged to the activity log system.

**Impact:** No audit trail for AI-driven changes.

---

#### 6. **MEDIUM: No Duplicate Detection**
**File:** `internal/services/ai.go:GenerateProposals()`

System can generate identical proposals repeatedly without checking for existing pending proposals with same parameters.

**Impact:** Proposal clutter; user fatigue from repetitive suggestions.

---

#### 7. **MEDIUM: No Proposal Expiration**
**File:** `internal/database/models.go`

No `expires_at` field or cleanup mechanism for stale pending proposals.

**Impact:** Old proposals accumulate indefinitely.

---

#### 8. **LOW: No Proposal Feedback Loop**
No mechanism to track proposal performance (e.g., did a buy_signal proposal lead to profitable trade?).

**Impact:** Cannot improve AI suggestions based on historical performance.

---

## Implementation Plan

### Phase 1: Critical Fixes (Required for Basic Functionality)

#### 1.1 Fix Cron Scheduling for Auto-Generation
**File:** `internal/cron/scheduler.go`

Add to `Start()` function:
```go
// Schedule proposal generation if enabled
interval := getProposalInterval()
if interval != "disabled" {
    id, err := scheduler.AddFunc(interval, func() {
        if err := runGenerateProposals(); err != nil {
            log.Printf("Proposal generation job failed: %v", err)
        }
    })
    if err != nil {
        log.Printf("Failed to schedule proposal generation: %v", err)
    } else {
        proposalJobID = id
        log.Printf("Proposal generation scheduled with interval: %s", interval)
    }
}
```

Add reschedule function for dynamic interval updates.

---

#### 1.2 Add LLM Configuration API Endpoints
**File:** `internal/handlers/ai.go` (or new `internal/handlers/llm.go`)

Add handlers:
```go
func GetLLMConfig(c *fiber.Ctx) error { ... }
func UpdateLLMConfig(c *fiber.Ctx) error { ... }
func TestLLMConfig(c *fiber.Ctx) error { ... }
```

Register in `cmd/server/main.go`:
```go
llm := api.Group("/llm")
llm.Get("/config", handlers.GetLLMConfig)
llm.Put("/config", handlers.UpdateLLMConfig)
llm.Post("/test", handlers.TestLLMConfig)
```

---

### Phase 2: Enhanced Functionality (Production Readiness)

#### 2.1 Add WebSocket Broadcast Functions
**File:** `internal/websocket/broadcaster.go`

Add functions:
```go
func BroadcastAIProposalNew(proposal interface{}) {
    Broadcast("ai_proposal_new", proposal)
}

func BroadcastAIProposalUpdated(proposal interface{}) {
    Broadcast("ai_proposal_updated", proposal)
}

func BroadcastAIProposalsBulk(proposals interface{}) {
    Broadcast("ai_proposals_bulk", proposals)
}
```

**File:** `frontend/src/hooks/useWebSocket.js` and `App.jsx`

Add event listeners for real-time proposal updates.

---

#### 2.2 Complete Buy/Sell Signal Handling
**File:** `internal/services/ai.go:ApproveProposal()`

Extend approval logic:
```go
switch proposal.ProposalType {
case "parameter_adjustment":
    // existing logic
    
case "buy_signal":
    // Parse symbol from proposal
    // Call trading service to open position
    // Log activity
    
case "sell_signal":
    // Parse symbol from proposal
    // Call trading service to close position
    // Log activity
    
case "risk_management":
    // Apply risk management action
}
```

---

#### 2.3 Integrate Activity Logging
**File:** `internal/services/ai.go`

Add activity log calls:
```go
func GenerateProposals() {
    // ... generation logic ...
    LogActivity("ai_proposals_generated", fmt.Sprintf("Generated %d proposals", count), details)
}

func ApproveProposal(id uint) {
    // ... approval logic ...
    LogActivity("ai_proposal_approved", fmt.Sprintf("Approved %s proposal", proposal.ProposalType), details)
}
```

---

#### 2.4 Add Duplicate Detection
**File:** `internal/services/ai.go:GenerateProposals()`

Before creating proposals, check for existing pending duplicates:
```go
func isDuplicateProposal(proposal database.AIProposal) bool {
    var existing database.AIProposal
    result := database.DB.Where(
        "proposal_type = ? AND parameter_key = ? AND new_value = ? AND status = ?",
        proposal.ProposalType, proposal.ParameterKey, proposal.NewValue, "pending"
    ).First(&existing)
    return result.Error == nil
}
```

---

### Phase 3: Polish & Advanced Features

#### 3.1 Add Proposal Expiration
**File:** `internal/database/models.go`

Add field:
```go
type AIProposal struct {
    // ... existing fields ...
    ExpiresAt *time.Time `json:"expires_at"`
}
```

**File:** `internal/cron/scheduler.go`

Add cleanup job:
```go
scheduler.AddFunc("0 0 * * * *", func() { // hourly
    database.DB.Where("status = ? AND expires_at < ?", "pending", time.Now()).
        Update("status", "expired")
})
```

---

#### 3.2 Add Proposal Performance Tracking
**File:** `internal/database/models.go`

Extend AIProposal model:
```go
type AIProposal struct {
    // ... existing fields ...
    RelatedPositionID *uint      `json:"related_position_id"`
    PerformancePnl    *float64   `json:"performance_pnl"`
    PerformanceStatus string     `json:"performance_status" gorm:"size:20;default:pending"` // pending/success/failure
}
```

**File:** `internal/services/trading.go`

When closing positions, check if related to an AI proposal and update performance.

---

#### 3.3 Add Proposal Voting/Confidence
Allow multiple AI proposals to be grouped with confidence scoring.

---

## Files to Modify

| File | Changes |
|------|---------|
| `internal/cron/scheduler.go` | Add proposal job scheduling, add cleanup job |
| `internal/handlers/ai.go` | Add LLM config endpoints |
| `internal/services/ai.go` | Complete buy/sell handling, add activity logging, add duplicate detection |
| `internal/websocket/broadcaster.go` | Add proposal broadcast functions |
| `cmd/server/main.go` | Register new LLM routes |
| `internal/database/models.go` | Add ExpiresAt, performance fields |
| `frontend/src/hooks/useWebSocket.js` | Add proposal event handlers |
| `frontend/src/App.jsx` | Integrate proposal WebSocket events |

---

## Testing Checklist

- [ ] Cron job schedules and runs proposal generation
- [ ] LLM config can be read/updated via API
- [ ] New proposals broadcast via WebSocket
- [ ] Buy/sell signal proposals execute trades on approval
- [ ] Activity logs created for all AI actions
- [ ] Duplicate proposals are filtered
- [ ] Expired proposals are cleaned up
- [ ] Proposal performance tracked for closed positions

---

## Future Enhancements

1. **Multi-provider LLM support** - Fallback to alternative providers
2. **Proposal templates** - Allow users to define custom proposal types
3. **A/B testing** - Compare different prompt strategies
4. **Proposal analytics dashboard** - Visualize AI performance over time
5. **Human-in-the-loop learning** - Use approval/denial patterns to fine-tune prompts
