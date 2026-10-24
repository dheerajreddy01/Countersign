import os

from fastapi import FastAPI
from pydantic import BaseModel, Field

from .scoring import Features, decide, score

app = FastAPI(title="Countersign AI service")

DEFAULT_COST_TO_FIGHT = float(os.getenv("COST_TO_FIGHT_DEFAULT", "25.00"))


class ScoreRequest(BaseModel):
    amount: float = Field(ge=0)
    delivery_confirmed: bool = False
    signature_captured: bool = False
    mandate_matching: bool = False
    prior_customer_contact: bool = False
    buyer_dispute_history: int = Field(default=0, ge=0)
    days_to_deadline: float = 0
    cost_to_fight: float | None = Field(default=None, ge=0)


class ScoreResponse(BaseModel):
    p_win: float
    ev_fight: float
    refund_cost: float
    recommendation: str
    top_factors: list[tuple[str, float]]


@app.get("/healthz")
def healthz() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/score", response_model=ScoreResponse)
def score_dispute(req: ScoreRequest) -> ScoreResponse:
    s = score(
        Features(
            delivery_confirmed=req.delivery_confirmed,
            signature_captured=req.signature_captured,
            mandate_matching=req.mandate_matching,
            prior_customer_contact=req.prior_customer_contact,
            buyer_dispute_history=req.buyer_dispute_history,
            amount=req.amount,
            days_to_deadline=req.days_to_deadline,
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
    )
