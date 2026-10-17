// Command kandev-plugin-shapeup is the backend half of the Shape Up plugin:
// cycles, bets and the circuit breaker. kandev spawns it as a gRPC
// subprocess; pluginsdk.Serve owns the transport.
package main

import (
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

func main() {
	pluginsdk.Serve(newPlugin(time.Now))
}
