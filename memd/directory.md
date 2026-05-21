# Configure Memory Directories

Use this protocol when creating, registering, initializing, repairing, renaming, or removing a memd memory directory.

## Registry

Memory directories are registered in `memd.md`.

Use this shape:

```yaml
memory_directories:
  - id: default
    path: ./default
    description: General memory for this repo.
    git: true
```

Required fields:

- `id`
- `path`
- `description`

Optional fields:

- `git`

Do not add branch, remote, repository, or hosting details unless the user explicitly asks. If `git: true`, infer Git behavior from the directory's local repository.

## Directory Structure

Each memory directory contains:

```text
README.md
MEMORY.md
memory/
  index.md
```

`README.md` describes the directory's purpose, when to use it, what not to store, write rules, and isolation rules.

`MEMORY.md` is the short active memory layer.

`memory/index.md` is the map of the deeper wiki.

Other pages under `memory/` emerge over time.

## Create A Directory

When the user asks to create a memory directory:

1. Infer `id`, `path`, and `description` from the request.
2. Ask only for missing or risky details.
3. Create the directory structure.
4. Add the directory to the `memd.md` registry.
5. Keep initial files generic and compact.
6. If `git: true`, commit the changes.

Example user request:

```text
Use memd to create a memory directory for public writing.
```

Reasonable inferred registry entry:

```yaml
memory_directories:
  - id: public-writing
    path: ./public-writing
    description: Memory for public writing voice, LinkedIn strategy, examples, rejected tones, and content direction.
    git: true
```

## Register An Existing Directory

When the user asks to use an existing folder:

1. Inspect the path.
2. If it has the required structure, register it in `memd.md`.
3. If it is empty, initialize it.
4. If it is partial, repair only the missing required files.
5. Ask before overwriting existing files.

## Remove A Directory

Removing a directory from memd usually means removing its registry entry, not deleting files.

Delete files only when the user explicitly asks.

## Rename A Directory

When renaming:

1. Update the registry.
2. Move the folder if requested.
3. Preserve memory contents.
4. Fix obvious links only when safe.

