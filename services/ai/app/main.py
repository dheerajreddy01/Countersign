import os

from fastapi import Depends, FastAPI
from pydantic import BaseModel, Field

from .classify import classify
from .draft import Evidence, draft_response
from .grounding import check_grounding
from .registry import ModelRegistry, get_registry
from .scoring import Features, decide, score

app = FastAPI(title="Countersign AI service")

DEFAULT_COST_TO_FIGHT = float(os.getenv("COST_TO_FIGHT_DEFAULT", "25.00"))

# The fraud signal is only meaningful when the buyer says they did not authorise the charge.
FRAUD_REASONS = {"unauthorized"}


class ScoreRequest(BaseModel):
    amount: float = Field(ge=0)
    reason: str | None = None
    delivery_confirmed: bool = False
    signature_captured: bool = False
    mandate_matching: bool = False
    prior_customer_contact: bool = False
    buyer_dispute_history: int = Field(default=0, ge=0)
    days_to_deadline: float = 0
    cost_to_fight: float | None = Field(default=None, ge=0)
    # Transaction-level fields for the fraud model (amount, product code, email domain, ...).
    fraud_features: dict[str, float | str] | None = None


class ScoreResponse(BaseModel):
    p_win: float
    ev_fight: float
    refund_cost: float
    recommendation: str
    top_factors: list[tuple[str, float]]
    fraud_likelihood: float | None = None
    model_versions: dict[str, str] = {}


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


# --- Dispute defence pipeline: classify, draft, grounding guard ---------------


class ClassifyRequest(BaseModel):
    reason: str | None = None


class ClassifyResponse(BaseModel):
    category: str
    supported: bool
    normalized_from: str


@app.post("/classify", response_model=ClassifyResponse)
def classify_reason(req: ClassifyRequest) -> ClassifyResponse:
    c = classify(req.reason)
    return ClassifyResponse(
        category=c.category, supported=c.supported, normalized_from=c.normalized_from
    )


class EvidenceIn(BaseModel):
    id: str
    type: str
    summary: str = ""


class DraftRequest(BaseModel):
    reason: str
    evidence: list[EvidenceIn] = []


class DraftResponse(BaseModel):
    category: str
    text: str
    citations: list[str]
    grounding_passed: bool
    uncited_sentences: list[str]
    claims_count: int


@app.post("/draft", response_model=DraftResponse)
def draft_dispute(req: DraftRequest) -> DraftResponse:
    d = draft_response(
        req.reason, [Evidence(id=e.id, type=e.type, summary=e.summary) for e in req.evidence]
    )
    return DraftResponse(
        category=classify(req.reason).category,
        text=d.text,
        citations=d.citations,
        grounding_passed=d.grounding_passed,
        uncited_sentences=d.uncited_sentences,
        claims_count=d.claims_count,
    )


class GroundRequest(BaseModel):
    text: str
    evidence_ids: list[str] = []


class GroundResponse(BaseModel):
    passed: bool
    uncited_sentences: list[str]
    citations: list[str]
    unknown_markers: list[str]


@app.post("/ground", response_model=GroundResponse)
def ground_text(req: GroundRequest) -> GroundResponse:
    """Authoritative grounding check, e.g. for a merchant-edited draft."""
    r = check_grounding(req.text, req.evidence_ids)
    return GroundResponse(
        passed=r.passed,
        uncited_sentences=r.uncited_sentences,
        citations=r.citations,
        unknown_markers=r.unknown_markers,
    )


@app.post("/score", response_model=ScoreResponse)
def score_dispute(req: ScoreRequest, registry: ModelRegistry = Depends(get_registry)) -> ScoreResponse:
    fraud_likelihood = None
    if registry.fraud and req.fraud_features and req.reason in FRAUD_REASONS:
        fraud_likelihood = min(max(registry.fraud.predict(req.fraud_features), 0.0), 1.0)

    s = score(
        Features(
            delivery_confirmed=req.delivery_confirmed,
            signature_captured=req.signature_captured,
            mandate_matching=req.mandate_matching,
            prior_customer_contact=req.prior_customer_contact,
            buyer_dispute_history=req.buyer_dispute_history,
            amount=req.amount,
            days_to_deadline=req.days_to_deadline,
            fraud_likelihood=fraud_likelihood,
        )
    )
    cost = DEFAULT_COST_TO_FIGHT if req.cost_to_fight is None else req.cost_to_fight
    d = decide(s.p_win, req.amount, cost)
    return ScoreResponse(
        p_win=s.p_win,
        ev_fight=d.ev_fight,
        refund_cost=d.refund_cost,
        recommendation=d.recommendation,
        top_factors=s.top_factors,
        fraud_likelihood=fraud_likelihood,
        model_versions=registry.versions() if fraud_likelihood is not None else {},
    )
