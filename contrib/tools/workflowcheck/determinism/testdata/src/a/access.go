package a

import (
	"crypto/rand"
	"fmt"
	"os"
	"time"
)

var BadVar time.Time // want BadVar:"declared non-deterministic"

func AccessesStdout() { // want AccessesStdout:"accesses non-deterministic var os.Stdout"
	os.Stdout.Write([]byte("Hello"))
}

func AccessesStdoutTransitively() { // want AccessesStdoutTransitively:"calls non-deterministic function a.AccessesStdout"
	AccessesStdout()
}

func CallsOtherStdoutCall() { // want CallsOtherStdoutCall:"calls non-deterministic function fmt.Println"
	fmt.Println()
}

func AccessesBadVar() { // want AccessesBadVar:"accesses non-deterministic var a.BadVar"
	BadVar.Day()
}

func AccessesIgnoredStderr() {
	os.Stderr.Write([]byte("Hello"))
}

func AccessesCryptoRandom() { // want AccessesCryptoRandom:"accesses non-deterministic var crypto/rand.Reader"
	rand.Reader.Read(nil)
}

func AccessesCryptoRandomTransitively() { // want AccessesCryptoRandomTransitively:"calls non-deterministic function crypto/rand.Read"
	rand.Read(nil)
}
