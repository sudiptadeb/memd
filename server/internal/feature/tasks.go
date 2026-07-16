package feature

// tasksFeature is the first built-in. Tasks are framed to the agent as a kind
// of memory it keeps for the user: things to do, with status and due dates.
var tasksFeature = Feature{
	Key:           "tasks",
	Name:          "Tasks",
	Folder:        "tasks",
	AgentSummary:  "things the user needs to do, with status (open/done) and optional due dates",
	baseDoctrine:  tasksBaseDoctrine,
	prefsTemplate: tasksPrefsTemplate,
	legacyPrefsTemplates: []string{
		tasksLegacyPrefsTemplate,
	},
}

const tasksBaseDoctrine = `Tasks are a kind of memory you keep in this directory on the user's behalf:
things they need to do, with status and optional due dates. They live in the ` + "`tasks/`" + ` folder.

How tasks are stored:
- A task is a Markdown checklist line: "- [ ] do the thing" (open) or "- [x] do the thing" (done).
- Loose tasks live in ` + "`tasks/inbox.md`" + `. Group related tasks into named list files, e.g. ` + "`tasks/home-renovation.md`" + `.
- Add detail by indenting under the task line: ` + "`due:YYYY-MM-DD`" + `, ` + "`prio:high|med|low`" + `, ` + "`#tags`" + `,
  sub-tasks ("- [ ] ..."), and free ` + "`note:`" + ` lines. Anything you cannot fit a convention to is kept as a note.
- When a task outgrows its line (real notes, many sub-tasks), promote it to its own file ` + "`tasks/<slug>.md`" + `
  and leave the original line as a link: "- [ ] [Paint the bedroom](paint-bedroom.md)". A promoted task carries
  ` + "`status`" + `, ` + "`due`" + `, ` + "`prio`" + ` as top-level keys in the file's front matter — the same ` + "`---`" + ` block memd
  manages (they sit alongside the ` + "`memd:`" + ` stats subtree, never a second block) — with the notes in the body.
- Filenames are stable, hyphenated slugs (` + "`home-renovation.md`" + `, ` + "`paint-bedroom.md`" + `) — never encode
  status, priority, or due dates in a filename.

Finding and summarising:
- To find tasks, search the folder: open tasks = "- [ ]", deadlines = "due:", topics = "#tag".
- Keep a front-page overview (the directory's MEMORY.md, or ` + "`tasks/_board.md`" + `): open work grouped by
  deadline/status, each line linking to where the task lives. The files are the source of truth — regenerate
  the overview from them rather than trusting a possibly-stale index.

Completing: switch "- [ ]" to "- [x]". Keep recently-completed tasks as a record; archive long-done lists
when a list grows noisy.`

// tasksPrefsTemplate is the scaffold for tasks/_feature.md. Guidance lives in
// an HTML comment so a file the user never edited carries no visible rules:
// memory_load's preference overlay strips comments, sees nothing left, and
// stays silent until the user (or agent) writes a real preference.
const tasksPrefsTemplate = `# Tasks — your preferences

<!-- Rules written here layer on top of memd's built-in task behavior; you or
the agent may edit this file freely. Add plain bullets below this comment, e.g.:

- Always schedule tasks to be done 1 hour earlier than the real deadline.
- Tag anything work-related with #work.
-->
`

// tasksLegacyPrefsTemplate is the pre-comment-era scaffold. Directories enabled
// before the template changed still hold this exact body; it carries no user
// preferences, so the preload treats it as empty rather than echoing it.
const tasksLegacyPrefsTemplate = `# Tasks — your preferences

These rules are layered on top of memd's built-in task behavior. Add your own;
you or the agent may edit this file freely. Examples:

- Always schedule tasks to be done 1 hour earlier than the real deadline.
- Tag anything work-related with #work.
`
