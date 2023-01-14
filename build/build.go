package build

import cid "github.com/ipfs/go-cid"

var GenesisCID cid.Cid

func init() {
	var err error
	GenesisCID, err = cid.Decode(genesisCID)
	if err != nil {
		panic(err)
	}
}
