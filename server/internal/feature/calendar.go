package feature

// calendarFeature is registered for discovery but not yet usable. Its file
// conventions (recurrence, timezones, all-day events) are still being designed;
// see docs/plans/2026-06-14-feature-folders-design.md.
var calendarFeature = Feature{
	Key:           "calendar",
	Name:          "Calendar",
	Folder:        "calendar",
	AgentSummary:  "Calendar — dates and events the user wants remembered.",
	ComingSoon:    true,
	baseDoctrine:  calendarBaseDoctrine,
	prefsTemplate: calendarPrefsTemplate,
}

const calendarBaseDoctrine = `Calendar is a kind of memory for dates and events, kept in the ` + "`calendar/`" + ` folder.

(Coming soon — the file conventions for events, recurrence, and timezones are still being designed.
For now, record dates as plain notes and they will be migrated when the calendar feature lands.)`

const calendarPrefsTemplate = `# Calendar — your preferences

Layered on top of memd's built-in calendar behavior. Add your own rules, e.g.:

- No events on Sundays.
`
