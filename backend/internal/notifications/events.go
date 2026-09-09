package notifications

const (
	ScopeUser            = "user"
	ScopeUserHWIDDevices = "user_hwid_devices"
	ScopeNode            = "node"
	ScopeService         = "service"
	ScopeErrors          = "errors"
	ScopeCRM             = "crm"
)

// User events
const (
	EventUserCreated            = "user.created"
	EventUserModified           = "user.modified"
	EventUserDeleted            = "user.deleted"
	EventUserRevoked            = "user.revoked"
	EventUserDisabled           = "user.disabled"
	EventUserEnabled            = "user.enabled"
	EventUserLimited            = "user.limited"
	EventUserExpired            = "user.expired"
	EventUserTrafficReset       = "user.traffic_reset"
	EventUserFirstConnected     = "user.first_connected"
	EventUserExpiration         = "user.expiration"
	EventUserBandwidthThreshold = "user.bandwidth_usage_threshold_reached"
	EventUserNotConnected       = "user.not_connected"
)

// User HWID device events
const (
	EventUserHWIDDeviceAdded   = "user_hwid_devices.added"
	EventUserHWIDDeviceDeleted = "user_hwid_devices.deleted"
)

// Node events
const (
	EventNodeCreated            = "node.created"
	EventNodeModified           = "node.modified"
	EventNodeDisabled           = "node.disabled"
	EventNodeEnabled            = "node.enabled"
	EventNodeDeleted            = "node.deleted"
	EventNodeConnectionLost     = "node.connection_lost"
	EventNodeConnectionRestored = "node.connection_restored"
	EventNodeTrafficNotify      = "node.traffic_notify"
)

// Service events
const (
	EventServicePanelStarted   = "service.panel_started"
	EventLoginAttemptFailed    = "service.login_attempt_failed"
	EventLoginAttemptSuccess   = "service.login_attempt_success"
	EventServiceSubpageChanged = "service.subpage_config_changed"
	EventApiTokenCreated       = "service.api_token_created"
	EventApiTokenDeleted       = "service.api_token_deleted"
)

// Error events
const (
	EventBandwidthMaxNotification = "errors.bandwidth_usage_threshold_reached_max_notifications"
)

// CRM events (Infra Billing)
const (
	EventInfraBillingIn7Days        = "crm.infra_billing_node_payment_in_7_days"
	EventInfraBillingIn48Hours      = "crm.infra_billing_node_payment_in_48hrs"
	EventInfraBillingIn24Hours      = "crm.infra_billing_node_payment_in_24hrs"
	EventInfraBillingDueToday       = "crm.infra_billing_node_payment_due_today"
	EventInfraBillingOverdue24Hours = "crm.infra_billing_node_payment_overdue_24hrs"
	EventInfraBillingOverdue48Hours = "crm.infra_billing_node_payment_overdue_48hrs"
	EventInfraBillingOverdue7Days   = "crm.infra_billing_node_payment_overdue_7_days"
)
