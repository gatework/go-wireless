package wireless

import (
	"math"
	"strconv"
	"strings"
)

type rubyFP struct {
	frac uint64
	exp  int
}

var rubyPowers = [...]rubyFP{
	{18054884314459144840, -1220}, {13451937075301367670, -1193},
	{10022474136428063862, -1166}, {14934650266808366570, -1140},
	{11127181549972568877, -1113}, {16580792590934885855, -1087},
	{12353653155963782858, -1060}, {18408377700990114895, -1034},
	{13715310171984221708, -1007}, {10218702384817765436, -980},
	{15227053142812498563, -954}, {11345038669416679861, -927},
	{16905424996341287883, -901}, {12595523146049147757, -874},
	{9384396036005875287, -847}, {13983839803942852151, -821},
	{10418772551374772303, -794}, {15525180923007089351, -768},
	{11567161174868858868, -741}, {17236413322193710309, -715},
	{12842128665889583758, -688}, {9568131466127621947, -661},
	{14257626930069360058, -635}, {10622759856335341974, -608},
	{15829145694278690180, -582}, {11793632577567316726, -555},
	{17573882009934360870, -529}, {13093562431584567480, -502},
	{9755464219737475723, -475}, {14536774485912137811, -449},
	{10830740992659433045, -422}, {16139061738043178685, -396},
	{12024538023802026127, -369}, {17917957937422433684, -343},
	{13349918974505688015, -316}, {9946464728195732843, -289},
	{14821387422376473014, -263}, {11042794154864902060, -236},
	{16455045573212060422, -210}, {12259964326927110867, -183},
	{18268770466636286478, -157}, {13611294676837538539, -130},
	{10141204801825835212, -103}, {15111572745182864684, -77},
	{11258999068426240000, -50}, {16777216000000000000, -24},
	{12500000000000000000, 3}, {9313225746154785156, 30},
	{13877787807814456755, 56}, {10339757656912845936, 83},
	{15407439555097886824, 109}, {11479437019748901445, 136},
	{17105694144590052135, 162}, {12744735289059618216, 189},
	{9495567745759798747, 216}, {14149498560666738074, 242},
	{10542197943230523224, 269}, {15709099088952724970, 295},
	{11704190886730495818, 322}, {17440603504673385349, 348},
	{12994262207056124023, 375}, {9681479787123295682, 402},
	{14426529090290212157, 428}, {10748601772107342003, 455},
	{16016664761464807395, 481}, {11933345169920330789, 508},
	{17782069995880619868, 534}, {13248674568444952270, 561},
	{9871031767461413346, 588}, {14708983551653345445, 614},
	{10959046745042015199, 641}, {16330252207878254650, 667},
	{12166986024289022870, 694}, {18130221999122236476, 720},
	{13508068024458167312, 747}, {10064294952495520794, 774},
	{14996968138956309548, 800}, {11173611982879273257, 827},
	{16649979327439178909, 853}, {12405201291620119593, 880},
	{9242595204427927429, 907}, {13772540099066387757, 933},
	{10261342003245940623, 960}, {15290591125556738113, 986},
	{11392378155556871081, 1013}, {16975966327722178521, 1039},
	{12648080533535911531, 1066},
}

// rubyFloat is a pure-Go translation of JSON gem 3.1.0's fpconv_dtoa.
// Keep its rounding and formatting together: generic shortest-roundtrip
// formatting is not byte-compatible for every binary64 value.
func rubyFloat(d float64) string {
	sign := ""
	if math.Signbit(d) {
		sign = "-"
	}
	if d == 0 {
		return sign + "0.0"
	}
	b := math.Float64bits(d)
	w := rubyFP{b & 0xfffffffffffff, int((b >> 52) & 0x7ff)}
	if w.exp != 0 {
		w.frac += 1 << 52
		w.exp -= 1075
	} else {
		w.exp = -1074
	}
	upper := rubyFP{(w.frac << 1) + 1, w.exp - 1}
	for upper.frac&(1<<53) == 0 {
		upper.frac <<= 1
		upper.exp--
	}
	upper.frac <<= 10
	upper.exp -= 10
	shift := 1
	if w.frac == 1<<52 {
		shift = 2
	}
	lower := rubyFP{(w.frac << shift) - 1, w.exp - shift}
	lower.frac <<= uint(lower.exp - upper.exp)
	lower.exp = upper.exp
	for w.frac&(1<<52) == 0 {
		w.frac <<= 1
		w.exp--
	}
	w.frac <<= 11
	w.exp -= 11
	approx := int(-float64(upper.exp+87) * 0.30102999566398114)
	idx := (approx + 348) / 8
	for {
		current := upper.exp + rubyPowers[idx].exp + 64
		if current < -60 {
			idx++
			continue
		}
		if current > -32 {
			idx--
			continue
		}
		break
	}
	k := -(-348 + idx*8)
	cp := rubyPowers[idx]
	w = fpMultiply(w, cp)
	upper = fpMultiply(upper, cp)
	lower = fpMultiply(lower, cp)
	lower.frac++
	upper.frac--
	digits, k := fpDigits(w, upper, lower, k)
	n := len(digits)
	exp := k + n - 1
	abs := exp
	if abs < 0 {
		abs = -abs
	}
	if k >= 0 && abs < 15 {
		return sign + string(digits) + strings.Repeat("0", k) + ".0"
	}
	if k < 0 && (k > -7 || abs < 10) {
		offset := n + k
		if offset <= 0 {
			return sign + "0." + strings.Repeat("0", -offset) + string(digits)
		}
		return sign + string(digits[:offset]) + "." + string(digits[offset:])
	}
	body := string(digits[:1])
	if n > 1 {
		body += "." + string(digits[1:])
	}
	es := "+"
	if exp < 0 {
		es = "-"
	}
	return sign + body + "e" + es + strconv.Itoa(abs)
}
func fpMultiply(a, b rubyFP) rubyFP {
	const mask uint64 = 0xffffffff
	ahbl := (a.frac >> 32) * (b.frac & mask)
	albh := (a.frac & mask) * (b.frac >> 32)
	albl := (a.frac & mask) * (b.frac & mask)
	ahbh := (a.frac >> 32) * (b.frac >> 32)
	tmp := (ahbl & mask) + (albh & mask) + (albl >> 32) + (1 << 31)
	return rubyFP{ahbh + (ahbl >> 32) + (albh >> 32) + (tmp >> 32), a.exp + b.exp + 64}
}
func fpRound(d []byte, delta, rem, kappa, frac uint64) {
	for rem < frac && delta-rem >= kappa && (rem+kappa < frac || frac-rem > rem+kappa-frac) {
		d[len(d)-1]--
		rem += kappa
	}
}
func fpDigits(w, upper, lower rubyFP, k int) ([]byte, int) {
	wf := upper.frac - w.frac
	delta := upper.frac - lower.frac
	shift := uint(-upper.exp)
	one := uint64(1) << shift
	part1 := upper.frac >> shift
	part2 := upper.frac & (one - 1)
	digits := make([]byte, 0, 18)
	kappa := 10
	for div := uint64(1000000000); kappa > 0; div /= 10 {
		digit := part1 / div
		if digit != 0 || len(digits) > 0 {
			digits = append(digits, byte(digit)+'0')
		}
		part1 -= digit * div
		kappa--
		tmp := (part1 << shift) + part2
		if tmp <= delta {
			k += kappa
			fpRound(digits, delta, tmp, div<<shift, wf)
			return digits, k
		}
	}
	unit := uint64(10)
	for {
		part2 *= 10
		delta *= 10
		kappa--
		digit := part2 >> shift
		if digit != 0 || len(digits) > 0 {
			digits = append(digits, byte(digit)+'0')
		}
		part2 &= one - 1
		if part2 < delta {
			k += kappa
			fpRound(digits, delta, part2, one, wf*unit)
			return digits, k
		}
		unit *= 10
	}
}
