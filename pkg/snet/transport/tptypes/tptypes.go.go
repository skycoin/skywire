package tptypes

const (
	// STCP is a type of a transport that works via TCP and resolves addresses using PK table.
	STCP = "stcp"
	// STCPR is a type of a transport that works via TCP and resolves addresses using address-resolver service.
	STCPR = "stcpr"
	// STCPH is a type of a transport that works via TCP, resolves addresses using address-resolver service,
	// and uses TCP hole punching.
	STCPH = "stcph"
	// SUDP is a type of a transport that works via UDP and resolves addresses using PK table.
	SUDP = "sudp"
	// SUDPR is a type of a transport that works via UDP and resolves addresses using address-resolver service.
	SUDPR = "sudpr"
	// SUDPH is a type of a transport that works via UDP, resolves addresses using address-resolver service,
	// and uses TCP hole punching.
	SUDPH = "sudph"
)
