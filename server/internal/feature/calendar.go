package feature

// calendarFeature is registered for discovery but not yet usable. Its file
// conventions (recurrence, timezones, all-day events) are still being designed;
// see docs/plans/2026-06-14-feature-folders-design.md.
var calendarFeature = Feature{
	Key:           "calendar",
	Name:          "Calendar",
	Folder:        "calendar",
	AgentSummary:  "dates and events the user wants remembered",
	ComingSoon:    true,
	baseDoctrine:  calendarBaseDoctrine,
	prefsTemplate: calendarPrefsTemplate,
	legacyPrefsTemplates: []string{
		calendarLegacyPrefsTemplate,
	},
}

const calendarBaseDoctrine = `Calendar is a kind of memory for dates and events, kept in the ` + "`calendar/`" + ` folder.

(Coming soon — the file conventions for events, recurrence, and timezones are still being designed.
For now, record dates as plain notes and they will be migrated when the calendar feature lands.)`

const calendarPrefsTemplate = `# Calendar — your preferences

<!-- Rules written here layer on top of memd's built-in calendar behavior; you
or the agent may edit this file freely. Add plain bullets below this comment, e.g.:

- No events on Sundays.
-->
`

// calendarLegacyPrefsTemplate is the pre-comment-era scaffold, kept so old
// scaffolded files are recognised as "no preferences set".
const calendarLegacyPrefsTemplate = `# Calendar — your preferences

Layered on top of memd's built-in calendar behavior. Add your own rules, e.g.:

- No events on Sundays.
`
