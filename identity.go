package ts43

type Identity struct {
	ICCID string
	IMSI  string
	MCC   string
	MNC   string
	GID1  string
}

func mccmnc(i Identity) string {
	return i.MCC + i.MNC
}
