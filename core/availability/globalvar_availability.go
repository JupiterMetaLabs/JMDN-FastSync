package availability

var (
	availability = false
)

func AmIAvailable() bool {
	return availability
}

func IAmAvailable() {
	availability = true
}

func IAmNotAvailable() {
	availability = false
}