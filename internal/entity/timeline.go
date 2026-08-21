package entity

type TimelineView struct {
	Scope    TenantScope
	Caller   string
	DeviceID string
	RoomID   string
	History  HistoryFilter
	Filter   RoomEventFilter
}

func (TimelineView) Validate() error { return nil }
