// Package audit owns canonical, redacted AI audit records and their hash chain.
//
// V1 retention is project-history retention: sealed AI audit records and their
// content-addressed blobs remain with the project database and its backups for
// the lifetime of the project. There is no automatic event/blob purge because
// doing so would break historical DraftPatch provenance. Credentials, hidden
// reasoning, unrelated files, SDK objects and raw transport traces are outside
// that retained record and must be rejected or irreversibly redacted before a
// draft crosses the persistence boundary.
package audit
