// Package diagbundle builds the diagnostics bundle of `art-ai diagnose` (#234):
// one tar.gz a self-hosting user can send to the maintainers when something
// fails, with enough evidence to find the problem and align the fake upstream,
// and nothing that belongs to the user.
//
// A bundle holds five categories, each described in its manifest.json entry
// (what it holds, how many entries, whether the size limit cut it):
//
//	runtime   the commit and the Catalog the process runs (revision, SHA-256)
//	settings  the names of the Catalog's runtime settings and whether each is
//	          set: never a value
//	failures  the newest failed tasks in the time range: failure class, upstream
//	          status and code, a short sanitized excerpt of the upstream error
//	logs      the log lines of the time range, scrubbed line by line
//	captures  the sanitized upstream exchange records of the opt-in capture
//	          (#233), unchanged
//
// Two behaviours keep it safe, and they apply to different categories:
//
//   - Scrubbing replaces. The categories made of free text (logs, failure
//     excerpts) are rewritten value by value (see Scrubber): credentials,
//     signed URLs, addresses, e-mail addresses, user identifiers and prompts
//     become placeholders. A log line the scrubbing cannot make acceptable to
//     the privacy scan is dropped and counted in the manifest, never shipped.
//   - Refusing stops. Every member of the bundle, as it will be written, is
//     scanned again with the independent privacy scan (privacyscan), and the
//     capture records are never rewritten: they were sanitized when recorded,
//     so a finding in one means the recording or the directory was damaged, and
//     the export stops without writing a file. The error names the file, the
//     path and the rule, never the value.
//
// The export is read-only: it opens the database read-only, only lists and
// reads the log and capture directories, and writes one new file, mode 0600,
// next to nothing else. It never starts the service, migrates, rotates or
// deletes anything, so it can run beside a live stack.
package diagbundle
