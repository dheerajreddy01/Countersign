// Package events defines the PayPal webhook payloads the pipeline consumes and
// the normalised dispute event it publishes onto cs.dispute.events.
//
// Field names follow PayPal's Customer Disputes v1 reference. Only the fields
// Countersign acts on are modelled; the raw event is carried verbatim on the
// normalised event so nothing is lost before it reaches the audit log.
package events

// Event types the pipeline reacts to. Anything else is acknowledged and
// ignored rather than dead-lettered, so an unrelated subscription on the same
// webhook does not stall the topic.
const (
	TypeDisputeCreated  = "CUSTOMER.DISPUTE.CREATED"
	TypeDisputeUpdated  = "CUSTOMER.DISPUTE.UPDATED"
	TypeDisputeResolved = "CUSTOMER.DISPUTE.RESOLVED"
)

// Webhook is the envelope PayPal POSTs to the gateway.
type Webhook struct {
	ID         string          `json:"id"`
	EventType  string          `json:"event_type"`
	CreateTime string          `json:"create_time"`
	Resource   DisputeResource `json:"resource"`
}

// Money is PayPal's amount shape. Value is a decimal string ("149.00") and is
// kept as a string all the way to the NUMERIC column: parsing it to a float
// here would round money for no reason.
type Money struct {
	CurrencyCode string `json:"currency_code"`
	Value        string `json:"value"`
}

// Buyer is the disputing buyer as PayPal reports them.
type Buyer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DisputedTransaction is one transaction the dispute covers. Countersign uses
// the first one: the demo path is a single-item order.
type DisputedTransaction struct {
	SellerTransactionID string `json:"seller_transaction_id"`
	InvoiceID           string `json:"invoice_id"`
	ItemName            string `json:"item_name"`
	Buyer               Buyer  `json:"buyer"`
}

// DisputeResource is the dispute itself, under the webhook's resource key.
type DisputeResource struct {
	DisputeID             string                `json:"dispute_id"`
	Reason                string                `json:"reason"`
	Status                string                `json:"status"`
	DisputeAmount         Money                 `json:"dispute_amount"`
	DisputeLifeCycleStage string                `json:"dispute_life_cycle_stage"`
	SellerResponseDueDate string                `json:"seller_response_due_date"`
	CreateTime            string                `json:"create_time"`
	UpdatedTime           string                `json:"updated_time"`
	DisputedTransactions  []DisputedTransaction `json:"disputed_transactions"`
}
