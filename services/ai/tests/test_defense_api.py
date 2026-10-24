from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)


def test_classify_endpoint_maps_a_paypal_code():
    body = client.post("/classify", json={"reason": "UNAUTHORISED"}).json()
    assert body == {
        "category": "unauthorized",
        "supported": True,
        "normalized_from": "UNAUTHORISED",
    }


def test_draft_endpoint_returns_a_grounded_response():
    body = client.post(
        "/draft",
        json={
            "reason": "MERCHANDISE_OR_SERVICE_NOT_RECEIVED",
            "evidence": [
                {"id": "E1", "type": "order"},
                {"id": "E2", "type": "tracking"},
            ],
        },
    ).json()
    assert body["category"] == "item_not_received"
    assert body["grounding_passed"] is True
    assert body["claims_count"] == 2
    assert "[E1]" in body["text"] and "[E2]" in body["text"]


def test_ground_endpoint_flags_an_uncited_claim():
    body = client.post(
        "/ground",
        json={
            "text": "The item was independently verified as authentic.",
            "evidence_ids": ["E1"],
        },
    ).json()
    assert body["passed"] is False
    assert len(body["uncited_sentences"]) == 1


def test_ground_endpoint_passes_a_cited_claim():
    body = client.post(
        "/ground",
        json={
            "text": "The order was captured for the full amount [E1].",
            "evidence_ids": ["E1"],
        },
    ).json()
    assert body["passed"] is True
    assert body["citations"] == ["E1"]
