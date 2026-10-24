from fastapi.testclient import TestClient

from app.main import app
from app.registry import ModelRegistry, get_registry


class FakeFraud:
    version = "test-1"

    def __init__(self, value: float) -> None:
        self.value = value

    def predict(self, features):
        return self.value


def client_with(registry: ModelRegistry) -> TestClient:
    app.dependency_overrides[get_registry] = lambda: registry
    return TestClient(app)


def teardown_function():
    app.dependency_overrides.clear()


BASE = {"amount": 100, "reason": "unauthorized", "fraud_features": {"TransactionAmt": 100.0}}


def test_without_model_fraud_signal_is_neutral():
    body = client_with(ModelRegistry()).post("/score", json=BASE).json()
    assert body["fraud_likelihood"] is None
    assert body["model_versions"] == {}


def test_fraud_model_lowers_win_probability():
    neutral = client_with(ModelRegistry()).post("/score", json=BASE).json()
    fraudy = client_with(ModelRegistry(FakeFraud(0.9))).post("/score", json=BASE).json()
    assert fraudy["fraud_likelihood"] == 0.9
    assert fraudy["model_versions"] == {"fraud": "test-1"}
    assert fraudy["p_win"] < neutral["p_win"]


def test_fraud_model_ignored_for_other_reasons():
    req = {**BASE, "reason": "item_not_received"}
    body = client_with(ModelRegistry(FakeFraud(0.9))).post("/score", json=req).json()
    assert body["fraud_likelihood"] is None


def test_out_of_range_prediction_is_clamped():
    body = client_with(ModelRegistry(FakeFraud(1.7))).post("/score", json=BASE).json()
    assert body["fraud_likelihood"] == 1.0
