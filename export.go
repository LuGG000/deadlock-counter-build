package main

// Export: Build -> Ingame-Import-String (Base64(Zstd(Protobuf CMsgHeroBuild))),
// Format wie in deadlock_build.py. Zstd ohne Fremdbibliothek: gueltiger Frame mit Raw-Bloecken.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Hero-IDs (m_HeroID aus heroes.vdata, Stand Build 6817), Schluessel = norm(Name).
var heroIDs = map[string]uint32{
	"abrams": 6, "apollo": 77, "baba": 88, "bebop": 15, "billy": 72, "calico": 16, "celeste": 81,
	"deadmandanny": 78, "drifter": 64, "dynamo": 11, "graves": 76, "greytalon": 17, "haze": 13,
	"holliday": 14, "infernus": 1, "ivy": 20, "kelvin": 12, "ladygeist": 4, "lash": 31,
	"mcginnis": 8, "mina": 63, "mirage": 52, "mokrill": 18, "nurseharrow": 87, "paige": 67,
	"paradox": 10, "pocket": 50, "ratking": 84, "rem": 79, "seven": 2, "shiv": 19, "silver": 80,
	"sinclair": 60, "solomon": 85, "thedoorman": 69, "venator": 65, "victor": 66, "vindicta": 3,
	"violet": 86, "viscous": 35, "vyper": 58, "warden": 25, "wraith": 7, "yamato": 27,
}

func murmur2(s string) uint32 {
	data := []byte(s)
	const m = 0x5BD1E995
	h := uint32(0x31415926) ^ uint32(len(data))
	i := 0
	for ; len(data)-i >= 4; i += 4 {
		k := binary.LittleEndian.Uint32(data[i:])
		k *= m
		k ^= k >> 24
		k *= m
		h *= m
		h ^= k
	}
	switch len(data) - i {
	case 3:
		h ^= uint32(data[i+2]) << 16
		fallthrough
	case 2:
		h ^= uint32(data[i+1]) << 8
		fallthrough
	case 1:
		h ^= uint32(data[i])
		h *= m
	}
	h ^= h >> 13
	h *= m
	h ^= h >> 15
	return h
}

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
func pbU(b []byte, field int, v uint64) []byte {
	return pbVarint(pbVarint(b, uint64(field)<<3), v)
}
func pbBytes(b []byte, field int, v []byte) []byte {
	b = pbVarint(pbVarint(b, uint64(field)<<3|2), uint64(len(v)))
	return append(b, v...)
}
func pbF32(b []byte, field int, f float32) []byte {
	b = pbVarint(b, uint64(field)<<3|5)
	return binary.LittleEndian.AppendUint32(b, math.Float32bits(f))
}

// Steam32-Account-ID des Autors. Steht NICHT im Quellcode, sondern in settings.json neben der exe:
//
//	{"author_account_id": 123456789}
//
// (die ID steckt in jedem eigenen Ingame-Export-String, Feld "author_account_id").
var authorAccountID uint64

func loadSettings() {
	b, err := os.ReadFile(filepath.Join(appDir(), "settings.json"))
	if err != nil {
		return
	}
	var st struct {
		Author uint64 `json:"author_account_id"`
	}
	if json.Unmarshal(b, &st) == nil {
		authorAccountID = st.Author
	}
}

type exCat struct {
	Name  string
	IDs   []uint32
	Notes []string
	W, H  float32
	Opt   bool // Kategorie ist im Spiel als "Optional" markiert
}

// buildProto erzeugt CMsgHeroBuild (Felder aufsteigend wie im Python-Script).
func buildProto(heroID uint32, name, desc string, cats []exCat, ts uint32, order []byte) []byte {
	var details []byte
	for _, c := range cats {
		var cm []byte
		for i, id := range c.IDs {
			var mod []byte
			mod = pbU(mod, 1, uint64(id))
			if i < len(c.Notes) && c.Notes[i] != "" {
				mod = pbBytes(mod, 2, []byte(c.Notes[i]))
			}
			cm = pbBytes(cm, 1, mod)
		}
		cm = pbBytes(cm, 2, []byte(c.Name))
		cm = pbF32(cm, 4, c.W)
		cm = pbF32(cm, 5, c.H)
		if c.Opt {
			cm = pbU(cm, 6, 1)
		}
		details = pbBytes(details, 1, cm)
	}
	if order != nil {
		details = pbBytes(details, 2, order)
	}
	var b []byte
	b = pbU(b, 1, uint64(ts%1000000+100)) // lokale Build-ID
	b = pbU(b, 2, uint64(heroID))
	if authorAccountID != 0 {
		b = pbU(b, 3, authorAccountID)
	}
	b = pbU(b, 4, uint64(ts))
	b = pbBytes(b, 5, []byte(name))
	b = pbBytes(b, 6, []byte(desc))
	b = pbU(b, 7, 0)
	b = pbBytes(b, 10, details)
	return b
}

// xxh64 (fuer das Content-Checksum-Feld des Zstd-Frames).
func xxh64(in []byte) uint64 {
	var p1, p2, p3, p4, p5 uint64 = 11400714785074694791, 14029467366897019727, 1609587929392839161, 9650029242287828579, 2870177450012600261
	rotl := func(x uint64, r uint) uint64 { return x<<r | x>>(64-r) }
	round := func(acc, in uint64) uint64 { return rotl(acc+in*p2, 31) * p1 }
	merge := func(acc, v uint64) uint64 { return (acc^round(0, v))*p1 + p4 }
	n := len(in)
	var h uint64
	i := 0
	if n >= 32 {
		v1, v2, v3, v4 := p1+p2, p2, uint64(0), -p1
		for ; i+32 <= n; i += 32 {
			v1 = round(v1, binary.LittleEndian.Uint64(in[i:]))
			v2 = round(v2, binary.LittleEndian.Uint64(in[i+8:]))
			v3 = round(v3, binary.LittleEndian.Uint64(in[i+16:]))
			v4 = round(v4, binary.LittleEndian.Uint64(in[i+24:]))
		}
		h = rotl(v1, 1) + rotl(v2, 7) + rotl(v3, 12) + rotl(v4, 18)
		h = merge(h, v1)
		h = merge(h, v2)
		h = merge(h, v3)
		h = merge(h, v4)
	} else {
		h = p5
	}
	h += uint64(n)
	for ; i+8 <= n; i += 8 {
		h ^= round(0, binary.LittleEndian.Uint64(in[i:]))
		h = rotl(h, 27)*p1 + p4
	}
	if i+4 <= n {
		h ^= uint64(binary.LittleEndian.Uint32(in[i:])) * p1
		h = rotl(h, 23)*p2 + p3
		i += 4
	}
	for ; i < n; i++ {
		h ^= uint64(in[i]) * p5
		h = rotl(h, 11) * p1
	}
	h ^= h >> 33
	h *= p2
	h ^= h >> 29
	h *= p3
	h ^= h >> 32
	return h
}

// zstdRaw: gueltiger Zstandard-Frame aus Raw-Bloecken (keine Kompression, jeder Decoder liest ihn).
func zstdRaw(data []byte) []byte {
	out := []byte{0x28, 0xB5, 0x2F, 0xFD}
	// Frame-Header: Single-Segment, Content-Size 4 Byte (FCS flag 2), Checksum an.
	out = append(out, 0x80|0x20|0x04) // FCS=2 (4 Byte), single segment, checksum
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	const maxBlock = 128 * 1024
	if len(data) == 0 {
		out = append(out, 0x01, 0x00, 0x00) // letzter leerer Raw-Block
	}
	for off := 0; off < len(data); off += maxBlock {
		end := off + maxBlock
		last := uint32(0)
		if end >= len(data) {
			end = len(data)
			last = 1
		}
		hdr := last | uint32(end-off)<<3 // Block_Type 0 = Raw
		out = append(out, byte(hdr), byte(hdr>>8), byte(hdr>>16))
		out = append(out, data[off:end]...)
	}
	return binary.LittleEndian.AppendUint32(out, uint32(xxh64(data)))
}

var _ = crc32.IEEE

type ExportReq struct {
	Hero    string            `json:"hero"`
	Name    string            `json:"name"`
	Build   Build             `json:"build"`   // Early = Lane-Items, Core = Endbuild in Kaufreihenfolge, Late = Alternativen
	Counter []string          `json:"counter"` // Rueckfall-Optionen
	Notes   map[string]string `json:"notes"`   // Item -> Anmerkung (z.B. Counter-Grund)
}
type ExportResp struct {
	Code    string    `json:"code"`
	HeroID  uint32    `json:"hero_id"`
	Items   int       `json:"items"`
	Unknown []string  `json:"unknown"`
	Skill   SkillInfo `json:"skill"`
}

func (d *Data) exportBuild(req ExportReq) (*ExportResp, error) {
	hid, ok := heroIDs[norm(req.Hero)]
	if !ok {
		return nil, fmt.Errorf("Hero-ID fuer %q unbekannt", req.Hero)
	}
	resp := &ExportResp{HeroID: hid}
	mk := func(cname string, names []string, w, h float32) exCat {
		c := exCat{Name: cname, W: w, H: h, Opt: cname != "Core Build"}
		for _, n := range names {
			it := d.ItemMap[norm(n)]
			if it == nil || it.IName == "" {
				resp.Unknown = append(resp.Unknown, n)
				continue
			}
			c.IDs = append(c.IDs, murmur2(it.IName))
			c.Notes = append(c.Notes, req.Notes[n])
			resp.Items++
		}
		return c
	}
	// Reihenfolge wie bei echten Ingame-Builds: "Core Build" zuerst = Haupt-Build.
	var cats []exCat
	for _, c := range []exCat{
		mk("Core Build", req.Build.Core, 1045.5, 286.5),
		mk("Early", req.Build.Early, 563.25, 315),
		mk("Late", req.Build.Late, 470.25, 318),
		mk("Counter Picks", req.Counter, 469.5, 319.5),
	} {
		if len(c.IDs) > 0 {
			cats = append(cats, c)
		}
	}
	name := req.Name
	if name == "" {
		name = "Counter Build"
	}
	so, sinfo := skillOrder(norm(req.Hero))
	resp.Skill = sinfo
	pb := buildProto(hid, name, "", cats, uint32(time.Now().Unix()), so)
	resp.Code = base64.StdEncoding.EncodeToString(zstdRaw(pb))
	return resp, nil
}

func (d *Data) exportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("X-DL") != "1" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req ExportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp, err := d.exportBuild(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, resp)
}
