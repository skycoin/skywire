package tptypes

const (
	// STCP is a type of a transport that works via TCP and resolves addresses using PK table.
	STCP = "stcp"
	// STCPR is a type of a transport that works via TCP and resolves addresses using address-resolver service.
	STCPR = "stcpr"
	// SUDPH is a type of a transport that works via UDP, resolves addresses using address-resolver service,
	// and uses UDP hole punching.
	SUDPH = "sudph"
)
