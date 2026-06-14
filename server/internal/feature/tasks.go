package feature

// tasksFeature is the first built-in. Tasks are framed to the agent as a kind
// of memory it keeps for the user: things to do, with status and due dates.
var tasksFeature = Feature{
	Key:           "tasks",
	Name:          "Tasks",
	Folder:        "tasks",
	AgentSummary:  "Tasks — things the user needs to do, with status (open/done) and optional due dates.",
	baseDoctrine:  tasksBaseDoctrine,
	prefsTemplate: tasksPrefsTemplate,
}

const tasksBaseDoctrine = `Tasks are a kind of memory you keep in this directory on the user's behalf:
things they need to do, with status and optional due dates. They live in the ` + "`tasks/`" + ` folder.

How tasks are stored:
- A task is a Markdown checklist line: "- [ ] do the thing" (open) or "- [x] do the thing" (done).
- Loose tasks live in ` + "`tasks/inbox.md`" + `. Group related tasks into named list files, e.g. ` + "`tasks/home-renovation.md`" + `.
- Add detail by indenting under the task line: ` + "`due:YYYY-MM-DD`" + `, ` + "`prio:high|med|low`" + `, ` + "`#tags`" + `,
  sub-tasks ("- [ ] ..."), and free ` + "`note:`" + ` lines. Anything you cannot fit a convention to is kept as a note.
- When a task outgrows its line (real notes, many sub-tasks), promote it to its own file ` + "`tasks/<slug>.md`" + `
  and leave the original line as a link: "- [ ] [Paint the bedroom](paint-bedroom.md)". Promoted task files
  carry YAML front matter (status, due, prio) with the notes in the body.
- Filenames are stable names only — never encode status, priority, or due dates in a filename.

Finding and summarising:
- To find tasks, search the folder: open tasks = "- [ ]", deadlines = "due:", topics = "#tag".
- Keep a front-page overview (the directory's MEMORY.md, or ` + "`tasks/_board.md`" + `): open work grouped by
  deadline/status, each line linking to where the task lives. The files are the source of truth — regenerate
  the overview from them rather than trusting a possibly-stale index.

Completing: switch "- [ ]" to "- [x]". Keep recently-completed tasks as a record; archive long-done lists
when a list grows noisy.`

const tasksPrefsTemplate = `# Tasks — your preferences

These rules are layered on top of memd's built-in task behavior. Add your own;
you or the agent may edit this file freely. Examples:

- Always schedule tasks to be done 1 hour earlier than the real deadline.
- Tag anything work-related with #work.
`
