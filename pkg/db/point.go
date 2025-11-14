package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Native-Planet/perigee/roller"
	perigee "github.com/Native-Planet/perigee/types"
	"github.com/ethereum/go-ethereum/common"
)

type AzimuthRank uint

const (
	GALAXY = AzimuthRank(iota)
	STAR
	PLANET
	MOON
	COMET
)

type AzimuthNumber uint32

var client *roller.Roller

// Get the natural parent of an Azimuth point.
func (p AzimuthNumber) Parent() AzimuthNumber {
	if p > 0xffff {
		// Planets' parent is their star
		return p & 0xffff
	} else if p > 0xff {
		// Stars' parent is their galaxy
		return p & 0xff
	}
	// Galaxies don't have a parent
	return 0
}

// Get the "rank" of an Azimuth point
func (p AzimuthNumber) Rank() AzimuthRank {
	if p < 0xff {
		return GALAXY
	} else if p <= 0xffff {
		return STAR
	} else {
		return PLANET
	}
}

type Point struct {
	Number AzimuthNumber `db:"azimuth_number"`

	// Nonces are for L2 to prevent replay attacks
	OwnerAddress      common.Address `db:"owner_address" json:"Ownership.Owner.Address"`
	OwnerNonce        uint32         `db:"owner_nonce" json:"Ownership.Owner.Nonce"`
	SpawnAddress      common.Address `db:"spawn_address" json:"Ownership.SpawnProxy.Address"`
	SpawnNonce        uint32         `db:"spawn_nonce" json:"Ownership.SpawnProxy.Nonce"`
	ManagementAddress common.Address `db:"management_address" json:"Ownership.ManagementProxy.Address"`
	ManagementNonce   uint32         `db:"management_nonce" json:"Ownership.ManagementProxy.Nonce"`
	VotingAddress     common.Address `db:"voting_address" json:"Ownership.VotingProxy.Address"`
	VotingNonce       uint32         `db:"voting_nonce" json:"Ownership.VotingProxy.Nonce"`
	TransferAddress   common.Address `db:"transfer_address" json:"Ownership.TransferProxy.Address"`
	TransferNonce     uint32         `db:"transfer_nonce" json:"Ownership.TransferProxy.Nonce"`

	Dominion int    `db:"dominion" json:"Dominion"`
	IsActive bool   `db:"is_active"`
	Rift     uint32 `db:"rift" json:"Network.Rift"`

	EncryptionKey      []byte `db:"encryption_key" json:"Network.Keys.Crypt"`
	AuthKey            []byte `db:"auth_key" json:"Network.Keys.Auth"`
	CryptoSuiteVersion uint32 `db:"crypto_suite_version" json:"Network.Keys.Suit"`
	Life               uint32 `db:"life" json:"Network.Keys.Life"`

	HasSponsor        bool          `db:"has_sponsor" json:"Network.Sponsor.Has"`
	Sponsor           AzimuthNumber `db:"sponsor" json:"Network.Sponsor.Who"`
	IsEscapeRequested bool          `db:"is_escape_requested" json:""`
	EscapeRequestedTo AzimuthNumber `db:"escape_requested_to" json:""`
}

func (p Point) MarshalJSON() ([]byte, error) {
	type Alias Point

	result, err := json.Marshal(&struct {
		EncryptionKey string `json:"EncryptionKey"`
		AuthKey       string `json:"AuthKey"`
		Alias
	}{
		EncryptionKey: hex.EncodeToString(p.EncryptionKey),
		AuthKey:       hex.EncodeToString(p.AuthKey),
		Alias:         (Alias)(p),
	})
	if err != nil {
		err = fmt.Errorf("encoding json: %w", err)
	}
	return result, err
}

func (db DB) GetPoint(azimuth_number AzimuthNumber) (Point, bool) {
	var ret Point
	err := db.DB.Get(&ret, `select * from points where azimuth_number = ?`, azimuth_number)
	if errors.Is(err, sql.ErrNoRows) {
		return Point{}, false
	} else if err != nil {
		panic(err)
	}
	return ret, true
}

func (db DB) GetPoints() ([]Point, bool) {
	var ret []Point
	err := db.DB.Select(&ret, `select * from points`)
	if errors.Is(err, sql.ErrNoRows) {
		return []Point{}, false
	} else if err != nil {
		panic(err)
	}
	return ret, true
}

type PointHistory struct {
	ID               uint64        `db:"rowid"`
	ContractName     string        `db:"contract"`
	TxHash           string        `db:"tx_hash"`
	SourceEventLogID uint64        `db:"source_event_log_id"`
	IntraLogIndex    uint64        `db:"intra_log_index"`
	AzimuthNumber    AzimuthNumber `db:"azimuth_number"`
	OperationName    string        `db:"operation"`
	HexData          string        `db:"hex_data"`
}

func (db DB) GetEventsForPoint(azimuth_number AzimuthNumber) (ret []PointHistory, is_ok bool) {
	err := db.DB.Select(&ret, `select * from readable_diffs where azimuth_number = ?`, azimuth_number)
	if errors.Is(err, sql.ErrNoRows) {
		return []PointHistory{}, false
	} else if err != nil {
		panic(err)
	}
	return ret, true
}

func isZeroOrEmpty(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func decodeRollerHexKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}

	// Trim 0x/0X if present
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")

	if s == "" {
		// "0x" with no data: treat as no key
		return nil, nil
	}

	// odd length -> pad
	if len(s)%2 == 1 {
		s = "0" + s
	}

	return hex.DecodeString(s)
}

func DiffDBPointWithRemote(dbp Point, rp perigee.Point) []string {
	diffs := []string{}

	// Dominion: db int vs API string
	if fmt.Sprintf("l%v", dbp.Dominion) != rp.Dominion {
		diffs = append(diffs, fmt.Sprintf("dominion: db=%d api=%d", dbp.Dominion, rp.Dominion))
	}

	// Owner address
	if !strings.EqualFold(dbp.OwnerAddress.Hex(), rp.Ownership.Owner.Address) {
		diffs = append(diffs, fmt.Sprintf("owner address: db=%s api=%s",
			dbp.OwnerAddress.Hex(), rp.Ownership.Owner.Address))
	}

	// Owner nonce
	if dbp.OwnerNonce != uint32(rp.Ownership.Owner.Nonce) {
		diffs = append(diffs, fmt.Sprintf("owner nonce: db=%d api=%d",
			dbp.OwnerNonce, rp.Ownership.Owner.Nonce))
	}

	// Management proxy
	if !strings.EqualFold(dbp.ManagementAddress.Hex(), rp.Ownership.ManagementProxy.Address) {
		diffs = append(diffs, fmt.Sprintf("mgmt address: db=%s api=%s",
			dbp.ManagementAddress.Hex(), rp.Ownership.ManagementProxy.Address))
	}
	if dbp.ManagementNonce != uint32(rp.Ownership.ManagementProxy.Nonce) {
		diffs = append(diffs, fmt.Sprintf("mgmt nonce: db=%d api=%d",
			dbp.ManagementNonce, rp.Ownership.ManagementProxy.Nonce))
	}

	// Spawn proxy
	if !strings.EqualFold(dbp.SpawnAddress.Hex(), rp.Ownership.SpawnProxy.Address) {
		diffs = append(diffs, fmt.Sprintf("spawn address: db=%s api=%s",
			dbp.SpawnAddress.Hex(), rp.Ownership.SpawnProxy.Address))
	}
	if dbp.SpawnNonce != uint32(rp.Ownership.SpawnProxy.Nonce) {
		diffs = append(diffs, fmt.Sprintf("spawn nonce: db=%d api=%d",
			dbp.SpawnNonce, rp.Ownership.SpawnProxy.Nonce))
	}

	// Transfer proxy
	if !strings.EqualFold(dbp.TransferAddress.Hex(), rp.Ownership.TransferProxy.Address) {
		diffs = append(diffs, fmt.Sprintf("transfer address: db=%s api=%s",
			dbp.TransferAddress.Hex(), rp.Ownership.TransferProxy.Address))
	}
	if dbp.TransferNonce != uint32(rp.Ownership.TransferProxy.Nonce) {
		diffs = append(diffs, fmt.Sprintf("transfer nonce: db=%d api=%d",
			dbp.TransferNonce, rp.Ownership.TransferProxy.Nonce))
	}

	// Rift
	if rift, err := strconv.ParseUint(rp.Network.Rift, 10, 32); err == nil {
		if dbp.Rift != uint32(rift) {
			diffs = append(diffs, fmt.Sprintf("rift: db=%d api=%d", dbp.Rift, rift))
		}
	} else {
		diffs = append(diffs, fmt.Sprintf("rift: invalid api value %q", rp.Network.Rift))
	}

	// Keys
	if crypt, err := decodeRollerHexKey(rp.Network.Keys.Crypt); err == nil {
		if isZeroOrEmpty(dbp.EncryptionKey) && isZeroOrEmpty(crypt) {
			// both effectively "no key" -> OK
		} else if !bytes.Equal(dbp.EncryptionKey, crypt) {
			diffs = append(diffs,
				fmt.Sprintf("encryption key mismatch: db: %v roller: %v", dbp.EncryptionKey, crypt))
		}
	} else {
		fmt.Printf("db: %v roller: %v rp: %v\n", dbp.EncryptionKey, nil, rp.Network.Keys.Crypt)
		diffs = append(diffs, "invalid crypt key in api")
	}

	if auth, err := decodeRollerHexKey(rp.Network.Keys.Auth); err == nil {
		if isZeroOrEmpty(dbp.AuthKey) && isZeroOrEmpty(auth) {
			// both effectively "no key" -> OK
		} else if !bytes.Equal(dbp.AuthKey, auth) {
			diffs = append(diffs,
				fmt.Sprintf("auth key mismatch: db: %v roller: %v", dbp.AuthKey, auth))
		}
	} else {
		fmt.Printf("db: %v roller: %v rp: %v\n", dbp.AuthKey, nil, rp.Network.Keys.Auth)
		diffs = append(diffs, "invalid auth key in api")
	}

	if life, err := strconv.ParseUint(rp.Network.Keys.Life, 10, 32); err == nil {
		if dbp.Life != uint32(life) {
			diffs = append(diffs, fmt.Sprintf("life: db=%d api=%d", dbp.Life, life))
		}
	} else {
		diffs = append(diffs, fmt.Sprintf("life: invalid api value %q", rp.Network.Keys.Life))
	}

	if suite, err := strconv.ParseUint(rp.Network.Keys.Suite, 10, 32); err == nil {
		if dbp.CryptoSuiteVersion != uint32(suite) {
			diffs = append(diffs, fmt.Sprintf("crypto suite: db=%d api=%d",
				dbp.CryptoSuiteVersion, suite))
		}
	} else {
		diffs = append(diffs, fmt.Sprintf("suite: invalid api value %q", rp.Network.Keys.Suite))
	}

	// Sponsor
	if dbp.HasSponsor != rp.Network.Sponsor.Has {
		diffs = append(diffs, fmt.Sprintf("hasSponsor: db=%v api=%v",
			dbp.HasSponsor, rp.Network.Sponsor.Has))
	}
	if rp.Network.Sponsor.Has {
		if dbp.Sponsor != AzimuthNumber(rp.Network.Sponsor.Who) {
			diffs = append(diffs, fmt.Sprintf("sponsor: db=%d api=%d",
				dbp.Sponsor, rp.Network.Sponsor.Who))
		}
	}

	// Wire in IsEscapeRequested/EscapeRequestedTo once you know where that lives in the remote JSON.

	return diffs
}

func (db DB) CheckPointsAgainstRoller(ctx context.Context) error {
	points, ok := db.GetPoints()
	if !ok || len(points) == 0 {
		fmt.Println("no points in DB")
		return nil
	}

	for _, p := range points {
		// Roller expects something like ship number or patp;
		// here I assume the numeric ship number is fine.
		remote, err := roller.Client.GetPoint(ctx, int(p.Number))
		if err != nil {
			return fmt.Errorf("roller GetPoint(%d): %w", p.Number, err)
		}

		// If types.Point matches your perigee.Point struct, you can:
		//   var rp perigee.Point
		//   // map fields from remote to rp
		// or just adjust DiffDBPointWithRemote to accept *types.Point.

		// Example assuming remote is JSON-compatible with perigee.Point:
		data, err := json.Marshal(remote)
		if err != nil {
			return fmt.Errorf("marshal remote point %d: %w", p.Number, err)
		}

		var rp perigee.Point
		if err := json.Unmarshal(data, &rp); err != nil {
			return fmt.Errorf("unmarshal into perigee.Point %d: %w", p.Number, err)
		}

		diffs := DiffDBPointWithRemote(p, rp)
		if len(diffs) > 0 {
			fmt.Printf("point %d mismatches:\n", p.Number)
			for _, d := range diffs {
				fmt.Printf("  - %s\n", d)
			}
		}
	}

	return nil
}
