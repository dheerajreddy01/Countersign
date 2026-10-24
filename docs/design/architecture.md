# Countersign: high-level architecture

Diagrams are Mermaid and render on GitHub. Topic and table names come from [PLAN.md](../../PLAN.md).

## System overview

```mermaid
flowchart LR
    subgraph PP[PayPal sandbox]
        PPW[Webhooks]
        PPA[Disputes API]
        PPO[Orders / Transactions]
    end

    subgraph AG[Demo agent]
        DA[Shopping agent]
        ML[Mandate ledger]
    end

    subgraph SV[Countersign services]
        GW[Gateway<br/>Go<br/>verify + dedupe]
        NM[Normalizer]
        CS[Case service]
        EB[Evidence builder]
        AI[AI service<br/>Python<br/>classify, score, EV,<br/>draft, grounding guard]
        API[Console API]
        EX[Action executor]
        SU[Scoring updater]
    end

    subgraph K[Kafka / Redpanda]
        T1([pp.webhooks.raw])
        T2([cs.dispute.events])
        T3([cs.evidence.requested])
        T4([cs.case.ready])
        T5([cs.decision.pending])
        T6([cs.action.requested])
        T7([cs.outcomes])
        T8([cs.dlq])
    end

    DB[(PostgreSQL<br/>cases, evidence_records, mandates,<br/>decisions, drafts, actions,<br/>outcomes, audit_log)]

    UI[Merchant console<br/>Next.js + Bryntum Scheduler]

    DA -->|places order| PPO
    DA -->|signs mandate| ML --> DB

    PPW --> GW --> T1 --> NM --> T2 --> CS --> T3 --> EB --> T4 --> AI --> T5 --> API
    API <--> UI
    API --> T6 --> EX --> PPA
    PPA -->|ruling| PPW
    EX --> T7 --> SU

    CS <--> DB
    EB <--> DB
    AI <--> DB
    API <--> DB
    EX <--> DB
    SU --> DB
    EB -.->|order, tracking, invoice| PPO
    EB -.->|mandate lookup| DB

    GW -.-> T8
    EX -.-> T8
```

## Dispute lifecycle

```mermaid
sequenceDiagram
    autonumber
    participant B as Buyer
    participant P as PayPal sandbox
    participant G as Gateway
    participant C as Case + evidence services
    participant A as AI service
    participant M as Merchant console
    participant X as Executor

    B->>P: Opens dispute
    P->>G: Webhook (CUSTOMER.DISPUTE.CREATED)
    G->>G: Verify signature, dedupe by event id
    G->>C: pp.webhooks.raw → cs.dispute.events
    C->>C: Create case row
    C->>C: Gather order, tracking, invoice, mandate, messages
    C->>A: cs.case.ready
    A->>A: Classify reason, score P(win), compute EV
    A->>A: Draft response, grounding guard checks citations
    A->>M: cs.decision.pending (recommendation + draft)
    M->>M: Merchant reviews, edits, approves
    M->>X: cs.action.requested
    X->>P: Evidence / offer / message / accept claim
    P-->>G: Ruling webhook
    G->>C: Outcome recorded
    C->>A: cs.outcomes → scoring update
```

## Components

| Component | Responsibility | Tech |
|---|---|---|
| Gateway | Receive PayPal webhooks, verify signature, dedupe, publish raw events | Go |
| Normalizer | Map PayPal events to internal dispute events | Go |
| Case service | Own the `cases` row and its state | Go |
| Evidence builder | Assemble hashed evidence records for each case | Go |
| AI service | Classify the dispute, score `P(win)`, compute EV, draft, run the grounding guard | Python |
| Console API | Serve the UI, receive approvals and edits | Go |
| Action executor | Call the Disputes API idempotently; retry; write outcomes | Go |
| Scoring updater | Apply outcomes to the scoring model | Python |
| Mandate ledger | Store signed, hash-chained mandates for agent orders | Go + Postgres |
| Merchant console | Case review, approve/edit, deadline board | Next.js, Bryntum Scheduler |

Language per service is the plan's stated direction (Go gateway, Python AI service); the rest of the split is a proposal.

## Design notes

- **Kafka between stages** gives replay and a dead-letter topic (`cs.dlq`), and keys by dispute id keep one dispute's events ordered.
- **Idempotency** is enforced at two points: the gateway dedupes webhooks by event id, and the executor uses an idempotency key in `actions` so a retry never double-submits.
- **Merchant approval gates every outbound action.** Nothing reaches the Disputes API without passing through the console.
- **Evidence is hashed and the mandate is hash-chained** (`prev_hash`, `signature`), so the proof shown to PayPal can be checked later.
- **Grounding guard** sits between the drafter and the console: uncited sentences are rejected before the merchant sees the draft.
