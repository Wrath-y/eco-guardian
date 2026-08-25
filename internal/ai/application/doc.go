// Package application owns human accept/discard commands and read projections.
// It is the only AI boundary allowed to request revision writes through existing
// application ports; it never publishes a release or activates a graph.
package application
