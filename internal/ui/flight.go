package ui

import "time"

// buildTimeout bounds one detached build or digest compute, so a wedged store cannot
// hold a session's builds (and every request waiting on them) forever. It is far above
// storeTimeout on purpose: a build that outlives its request is the case it exists for.
const buildTimeout = 2 * time.Minute
