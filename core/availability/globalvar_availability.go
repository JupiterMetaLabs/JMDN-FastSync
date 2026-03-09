package availability

import "sync"

var (
	fastsyncAvailability *fastsyncready
	once                 sync.Once
)

type fastsyncready struct {
	availability bool
}

func FastsyncReady() *fastsyncready {
	once.Do(func() {
		fastsyncAvailability = &fastsyncready{
			availability: false,
		}
	})
	return fastsyncAvailability
}

func (fs *fastsyncready) AmIAvailable() bool {
	return fs.availability
}

func (fs *fastsyncready) IAmAvailable() {
	fs.availability = true
}

func (fs *fastsyncready) IAmNotAvailable() {
	fs.availability = false
}