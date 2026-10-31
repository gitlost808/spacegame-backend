package namefilter_test

import (
	"testing"

	"spacegame-backend/internal/namefilter"
)

func TestDisallowedEnglishAndGermanNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Fuck", "xxFuCkxx", "f.u.c.k", "f_u_c_k", "f-u-c-k", "f u c k",
		"f\u200buck", "f\u2060uck", "ｆｕｃｋ", "𝐅𝐮𝐜𝐤", "fück",
		"sh1t", "$h!t", "a$$h0le", "motherfucker", "white power",
		"Scheiße", "SCHEISSE", "scheiss", "sch3i$$3", "Sch3i$$e",
		"Hurensohn", "hürensöhn", "ＡＲＳＣＨＬＯＣＨ", "f!ckdich", "xFotze42", "W1chser",
		"ass", "Captain ass", "a s s", "HURE",
		"ScunthorpeFuck", "PushItShit", "NutterFotze", "shitYoshitaka",
	} {
		t.Run(name, func(t *testing.T) {
			if !namefilter.Disallowed(name) {
				t.Errorf("offensive name %q was accepted", name)
			}
		})
	}
}

func TestInnocentNamesAndUnicodeRemainAllowed(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"Pilot 🚀", "Müller", "Mu\u0308ller", "Grüße", "Großadmiral", "Schütze", "Füchschen",
		"Assassin", "Assassine", "ClassicPilot", "Classy", "BassPlayer", "Dickinson",
		"Hancock", "Cockpit", "Analyst", "Analysis", "Penistone", "Fagott",
		"Marschall", "Barsch", "Constitution", "Scunthorpe", "CaptainScunthorpe",
		"Push It", "RushIt", "Yoshitaka", "Nutter", "John Doe", "東京のパイロット", "José",
		"QueerPilot", "Gaymer", "HomoSapiens", "LesbianPilot",
		"88", "Pilot88", "Julia88", "Müller1988", "Nazia88", "Nazim88", "Ignazio88",
		"Adolf", "Adolf Müller", "Adolf Müller88", "Adolf1988", "Heil", "Heilmann88",
		"Hilter", "Hiltermann88", "Hubertus Heil", "Heilung", "NeonPilot",
		"ID214880", "ID188140", "Pilot14188", "SS Pilot", "HH88",
	} {
		t.Run(name, func(t *testing.T) {
			if namefilter.Disallowed(name) {
				t.Errorf("innocent name %q was rejected", name)
			}
		})
	}
}

func TestExtremistReferencesAreRejected(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"ADOLF HILTER 88 HEIL", "Adolf Hitler", "Adolf 88 Hilter", "HEIL 88 HILTER ADOLF",
		"ADOLF H I L T E R 88 HEIL", "AD0LF HILTER", "Adolph Hilter", "Heil Hilter",
		"Hilter88", "88 x Hilter", "xxHilter88xx", "PilotHilter88", "88HilterPilot",
		"Adolf Hitlar", "Heil Hitier", "Adolf Hittler",
		"xxH1TL3R88xx", "H\u200bitler", "ＨＩＴＬＥＲ", "ScunthorpeHitler",
		"Sieg Heil", "S13g H31l", "ＳＩＥＧ＿ＨＥＩＬ", "Neo_Nazi", "N4Z188", "Nazi",
		"1488", "14/88", "14-88", "14 88", "xX1488Xx", "Pilot1488", "8814", "８８１４",
		"Ku Klux Klan", "WhiteSupremacy", "Nationalsozialist", "BlutUndEhre",
		"Meine Ehre heißt Treue", "UnsereEhreHeisstTreue", "Judenvergaser",
	} {
		t.Run(name, func(t *testing.T) {
			if !namefilter.Disallowed(name) {
				t.Errorf("extremist reference %q was accepted", name)
			}
		})
	}
}
