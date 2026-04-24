package urlfetch

// testMaxBytes is the generous cap value used by tests that aren't
// exercising the cap-enforcement path itself — 4 GiB is larger than any
// fixture a unit test generates, so call sites pass this value to Fetch /
// runHLSRemux without worrying about accidental rejection.
const testMaxBytes = int64(4) << 30
