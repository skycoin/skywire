# calvin
convert text to Calvin S ascii font (https://patorjk.com/software/taag/#p=display&amp;f=Calvin%20S&amp;t=)


example:

```
$ echo "Hello, World!" | go run cmd/calvin/calvin.go
╦ ╦ ┌─┐┬  ┬  ┌─┐   ╦ ╦ ┌─┐┬─┐┬  ┌┬┐┬    
╠═╣ ├┤ │  │  │ │   ║║║ │ │├┬┘│   │││    
╩ ╩ └─┘┴─┘┴─┘└─┘┘  ╚╩╝ └─┘┴└─┴─┘─┴┘o    

```

library usage example

```
package main

import (
	"github.com/0magnet/calvin"
)

func main() {
	println(calvin.AsciiFont("Hello, World!"))
}

```
