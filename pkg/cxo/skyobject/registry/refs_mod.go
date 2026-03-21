package registry

// flags of modifications
type refsMod int

const (
	loadedMod  refsMod = 1 << iota // has been loaded
	lengthMod                      // length has been modified
	contentMod                     // content has been modified
	originMod                      // the Refs is not the same as it was loaded
)
