package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type storeItem struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	PriceCents  int    `json:"price_cents"`
	Type        string `json:"type"`
	Benefit     string `json:"benefit"`
}

type userPurchase struct {
	PurchaseID uint
	ItemName   string
	Benefit    string
	ItemType   string
	ExpiresAt  *time.Time
}

type tagRequestStatus struct {
	Status        string
	RejectReason  string
	Name          string
	Color         string
	NextRequestAt *time.Time
}

func (h *Handlers) computeTagCooldown(userID uint) *time.Time {
	var endStr string
	err := h.db.QueryRow(`
		SELECT DATE_FORMAT(DATE_ADD(tag_requested_at, INTERVAL 7 DAY), '%Y-%m-%d %H:%i:%s')
		FROM users
		WHERE id = ? AND tag_requested_at > DATE_SUB(NOW(), INTERVAL 7 DAY)`, userID).Scan(&endStr)
	if err != nil {
		return nil
	}
	ts, err := time.Parse("2006-01-02 15:04:05", endStr)
	if err != nil {
		return nil
	}
	return &ts
}

type storePageData struct {
	Items        []storeItem
	Purchases    []userPurchase
	HasSupporter bool
	HasPriority  bool
	TagRequest   *tagRequestStatus
	PayPalReady  bool
	FlashMsg     string
	FlashOK      bool
}

func (h *Handlers) StorePage(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	storeActive := h.storeEnabled()

	data := storePageData{PayPalReady: paypalEnabled() && storeActive}

	if storeActive {
		rows, err := h.db.Query(
			`SELECT id, name, slug, description, price_cents, type, benefit FROM store_items WHERE active = 1 ORDER BY sort_order`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var it storeItem
				if rows.Scan(&it.ID, &it.Name, &it.Slug, &it.Description, &it.PriceCents, &it.Type, &it.Benefit) == nil {
					data.Items = append(data.Items, it)
				}
			}
		}
	}

	if u != nil {
		prows, _ := h.db.Query(`
			SELECT p.id, i.name, i.benefit, i.type, p.expires_at
			FROM purchases p JOIN store_items i ON i.id = p.item_id
			WHERE p.user_id = ? AND p.status = 'completed'
			  AND (p.expires_at IS NULL OR p.expires_at > NOW())
			ORDER BY p.completed_at DESC`, u.ID)
		if prows != nil {
			defer prows.Close()
			for prows.Next() {
				var up userPurchase
				if prows.Scan(&up.PurchaseID, &up.ItemName, &up.Benefit, &up.ItemType, &up.ExpiresAt) == nil {
					data.Purchases = append(data.Purchases, up)
				}
			}
		}
		data.HasSupporter = h.hasActivePurchase(u.ID, "supporter")
		data.HasPriority = h.hasActivePurchase(u.ID, "queue_priority")

		data.TagRequest = h.queryTagRequest(u.ID)

		switch r.URL.Query().Get("status") {
		case "success":
			data.FlashMsg = h.t(r, "store.flash.success")
			data.FlashOK = true
		case "cancelled":
			data.FlashMsg = h.t(r, "store.flash.cancelled")
			data.FlashOK = false
		case "error":
			data.FlashMsg = h.t(r, "error.server")
			data.FlashOK = false
		}
	}

	h.render(w, r, "store", data)
}

func (h *Handlers) StorePurchaseCancel(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var body struct {
		PurchaseID uint `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PurchaseID == 0 {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(`
		UPDATE purchases p
		JOIN store_items i ON i.id = p.item_id
		SET p.expires_at = NOW()
		WHERE p.id = ? AND p.user_id = ? AND p.status = 'completed'
		  AND i.type IN ('monthly','bimonthly')
		  AND (p.expires_at IS NULL OR p.expires_at > NOW())`,
		body.PurchaseID, u.ID)
	if err != nil {
		writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSONError(w, h.t(r, "error.store_sub_not_found"), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) StoreTagColorUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.storeEnabled() {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	if !h.hasActivePurchase(u.ID, "supporter") {
		writeJSONError(w, h.t(r, "error.store_supporter_required"), http.StatusForbidden)
		return
	}

	var body struct {
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	if len(body.Color) != 7 || body.Color[0] != '#' {
		writeJSONError(w, h.t(r, "error.store_color_format"), http.StatusBadRequest)
		return
	}

	var requestID uint
	var tagName string
	if err := h.db.QueryRow(
		`SELECT id, name FROM custom_tag_requests WHERE user_id = ? AND status = 'approved' ORDER BY created_at DESC LIMIT 1`, u.ID,
	).Scan(&requestID, &tagName); err != nil {
		writeJSONError(w, h.t(r, "error.tag_no_approved"), http.StatusBadRequest)
		return
	}

	var recentChanges int
	h.db.QueryRow(
		`SELECT COUNT(*) FROM tag_color_changes WHERE user_id = ? AND changed_at > DATE_SUB(NOW(), INTERVAL 30 MINUTE)`, u.ID,
	).Scan(&recentChanges)
	if recentChanges >= 5 {
		writeJSONError(w, h.t(r, "error.tag_color_cooldown"), http.StatusTooManyRequests)
		return
	}

	h.db.Exec(`UPDATE custom_tag_requests SET color = ? WHERE id = ?`, body.Color, requestID)
	h.db.Exec(`UPDATE tags SET color = ? WHERE name = ?`, body.Color, tagName)
	h.db.Exec(`INSERT INTO tag_color_changes (user_id) VALUES (?)`, u.ID)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (h *Handlers) StoreCheckout(w http.ResponseWriter, r *http.Request) {
	if !h.storeEnabled() {
		http.NotFound(w, r)
		return
	}
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	slug := strings.TrimSpace(r.FormValue("item"))
	if slug == "" {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	var item storeItem
	if err := h.db.QueryRow(
		`SELECT id, name, slug, description, price_cents, type, benefit FROM store_items WHERE slug = ? AND active = 1`, slug,
	).Scan(&item.ID, &item.Name, &item.Slug, &item.Description, &item.PriceCents, &item.Type, &item.Benefit); err != nil {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	if item.Type == "one_time" && h.hasActivePurchase(u.ID, item.Benefit) {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	scheme := "https"
	if strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s", scheme, r.Host)

	order, err := createPayPalOrder(item.PriceCents, item.Name, base+"/store/return", base+"/store/cancel")
	if err != nil {
		log.Printf("store: create PayPal order: %v", err)
		http.Redirect(w, r, "/store?status=error", http.StatusSeeOther)
		return
	}

	if _, err := h.db.Exec(
		`INSERT INTO purchases (user_id, item_id, paypal_order_id, status) VALUES (?, ?, ?, 'pending')`,
		u.ID, item.ID, order.ID,
	); err != nil {
		log.Printf("store: insert purchase: %v", err)
		http.Redirect(w, r, "/store?status=error", http.StatusSeeOther)
		return
	}

	for _, link := range order.Links {
		if link.Rel == "approve" {
			http.Redirect(w, r, link.Href, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/store?status=error", http.StatusSeeOther)
}

func (h *Handlers) StoreReturn(w http.ResponseWriter, r *http.Request) {
	if !h.storeEnabled() {
		http.NotFound(w, r)
		return
	}
	u := h.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	orderID := r.URL.Query().Get("token")
	if orderID == "" {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	var purchaseID uint
	var itemID uint
	var currentStatus string
	if err := h.db.QueryRow(
		`SELECT id, item_id, status FROM purchases WHERE paypal_order_id = ? AND user_id = ? LIMIT 1`,
		orderID, u.ID,
	).Scan(&purchaseID, &itemID, &currentStatus); err != nil {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}
	// Webhook may have already captured and completed this purchase.
	if currentStatus == "completed" {
		http.Redirect(w, r, "/store?status=success", http.StatusSeeOther)
		return
	}
	if currentStatus != "pending" {
		http.Redirect(w, r, "/store", http.StatusSeeOther)
		return
	}

	if _, err := capturePayPalOrder(orderID); err != nil {
		log.Printf("store: capture PayPal order %s: %v", orderID, err)
		h.db.Exec(`UPDATE purchases SET status = 'cancelled' WHERE id = ? AND status = 'pending'`, purchaseID)
		http.Redirect(w, r, "/store?status=cancelled", http.StatusSeeOther)
		return
	}

	var benefit, itemType string
	h.db.QueryRow(`SELECT benefit, type FROM store_items WHERE id = ?`, itemID).Scan(&benefit, &itemType)

	var expiresAt *time.Time
	switch itemType {
	case "monthly":
		exp := time.Now().Add(30 * 24 * time.Hour)
		expiresAt = &exp
	case "bimonthly":
		exp := time.Now().Add(60 * 24 * time.Hour)
		expiresAt = &exp
	}

	h.db.Exec(
		`UPDATE purchases SET status = 'completed', completed_at = NOW(), expires_at = ? WHERE id = ? AND status = 'pending'`,
		expiresAt, purchaseID,
	)
	http.Redirect(w, r, "/store?status=success", http.StatusSeeOther)
}

func (h *Handlers) StoreCancel(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u != nil {
		orderID := r.URL.Query().Get("token")
		if orderID != "" {
			h.db.Exec(
				`UPDATE purchases SET status = 'cancelled' WHERE paypal_order_id = ? AND user_id = ? AND status = 'pending'`,
				orderID, u.ID,
			)
		}
	}
	http.Redirect(w, r, "/store?status=cancelled", http.StatusSeeOther)
}

// StoreWebhook handles CHECKOUT.ORDER.APPROVED from PayPal.
// It captures and completes the purchase when the user approves payment but
// the browser redirect back to /store/return never arrives.
// Always returns 200 so PayPal does not retry; signature failure is logged and ignored.
func (h *Handlers) StoreWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !verifyPayPalWebhook(r.Header, body) {
		log.Println("store: webhook signature verification failed")
		w.WriteHeader(http.StatusOK)
		return
	}
	var event struct {
		EventType string `json:"event_type"`
		Resource  struct {
			ID string `json:"id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(body, &event); err != nil || event.EventType != "CHECKOUT.ORDER.APPROVED" {
		w.WriteHeader(http.StatusOK)
		return
	}
	orderID := event.Resource.ID
	if orderID == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var purchaseID uint
	var itemID uint
	var status string
	if err := h.db.QueryRow(
		`SELECT id, item_id, status FROM purchases WHERE paypal_order_id = ? LIMIT 1`, orderID,
	).Scan(&purchaseID, &itemID, &status); err != nil || status != "pending" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if _, err := capturePayPalOrder(orderID); err != nil {
		log.Printf("store: webhook capture %s: %v", orderID, err)
		w.WriteHeader(http.StatusOK)
		return
	}

	var benefit, itemType string
	h.db.QueryRow(`SELECT benefit, type FROM store_items WHERE id = ?`, itemID).Scan(&benefit, &itemType)

	var expiresAt *time.Time
	switch itemType {
	case "monthly":
		exp := time.Now().Add(30 * 24 * time.Hour)
		expiresAt = &exp
	case "bimonthly":
		exp := time.Now().Add(60 * 24 * time.Hour)
		expiresAt = &exp
	}
	h.db.Exec(
		`UPDATE purchases SET status = 'completed', completed_at = NOW(), expires_at = ? WHERE id = ? AND status = 'pending'`,
		expiresAt, purchaseID,
	)
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) StoreTagRequestSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.storeEnabled() {
		writeJSONError(w, h.t(r, "error.not_found"), http.StatusNotFound)
		return
	}
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}
	if !h.hasActivePurchase(u.ID, "supporter") {
		writeJSONError(w, h.t(r, "error.store_supporter_required"), http.StatusForbidden)
		return
	}

	var body struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, h.t(r, "error.invalid_json"), http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) == 0 || len(body.Name) > 32 {
		writeJSONError(w, h.t(r, "error.store_tag_name_length"), http.StatusBadRequest)
		return
	}
	if body.Color == "" {
		body.Color = "#cccccc"
	}

	var existingID uint
	var existingStatus string
	h.db.QueryRow(
		`SELECT id, status FROM custom_tag_requests WHERE user_id = ? AND status IN ('pending','approved','revision') ORDER BY created_at DESC LIMIT 1`, u.ID,
	).Scan(&existingID, &existingStatus)

	switch existingStatus {
	case "pending", "approved":
		writeJSONError(w, h.t(r, "error.store_tag_pending"), http.StatusConflict)
		return
	case "revision":
		if _, err := h.db.Exec(
			`UPDATE custom_tag_requests SET name = ?, color = ?, status = 'pending', reject_reason = '', reviewed_by = NULL, reviewed_at = NULL WHERE id = ?`,
			body.Name, body.Color, existingID,
		); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
	default:
		var cooldownActive int
		h.db.QueryRow(`
			SELECT CASE WHEN tag_requested_at > DATE_SUB(NOW(), INTERVAL 7 DAY) THEN 1 ELSE 0 END
			FROM users WHERE id = ?`, u.ID).Scan(&cooldownActive)
		if cooldownActive == 1 {
			writeJSONError(w, h.t(r, "error.tag_cooldown"), http.StatusTooManyRequests)
			return
		}
		if _, err := h.db.Exec(
			`INSERT INTO custom_tag_requests (user_id, name, color) VALUES (?, ?, ?)`,
			u.ID, body.Name, body.Color,
		); err != nil {
			writeJSONError(w, h.t(r, "error.db"), http.StatusInternalServerError)
			return
		}
		h.db.Exec(`UPDATE users SET tag_requested_at = NOW() WHERE id = ?`, u.ID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}
