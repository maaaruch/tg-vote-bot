package storage

import (
	"database/sql"
	"embed"
	"errors"
	"strings"
	"time"

	"tg-vote-bot/internal/domain"
)

//go:embed schema.sql
var embeddedSchema embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) InitSchema() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}

	b, err := embeddedSchema.ReadFile("schema.sql")
	if err != nil {
		return err
	}

	schema := strings.TrimSpace(string(b))
	_, err = s.db.Exec(schema)
	return err
}

// ---------- Rooms ----------

func (s *Store) CreateRoom(ownerID int64, title, password string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO rooms(owner_user_id, title, password) VALUES (?, ?, ?)`, ownerID, title, password)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListRoomsByOwner(ownerID int64) ([]domain.Room, error) {
	rows, err := s.db.Query(`SELECT id, title FROM rooms WHERE owner_user_id = ? ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []domain.Room
	for rows.Next() {
		var r domain.Room
		r.OwnerUserID = ownerID
		if err := rows.Scan(&r.ID, &r.Title); err != nil {
			return nil, err
		}
		rooms = append(rooms, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rooms, nil
}

func (s *Store) GetRoomByIDAndPassword(id int64, password string) (*domain.Room, error) {
	row := s.db.QueryRow(`SELECT id, owner_user_id, title, password, created_at FROM rooms WHERE id = ? AND password = ?`, id, password)
	var r domain.Room
	if err := row.Scan(&r.ID, &r.OwnerUserID, &r.Title, &r.Password, &r.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) IsRoomOwner(roomID, userID int64) (bool, error) {
	var cnt int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM rooms WHERE id = ? AND owner_user_id = ?`, roomID, userID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

func (s *Store) GetRoomTitle(roomID int64) (string, error) {
	var title string
	err := s.db.QueryRow(`SELECT title FROM rooms WHERE id = ?`, roomID).Scan(&title)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	return title, nil
}

// ---------- Nominations ----------

func (s *Store) ListNominations(roomID int64) ([]domain.Nomination, error) {
	rows, err := s.db.Query(`SELECT id, name, description FROM nominations WHERE room_id = ? ORDER BY id`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var noms []domain.Nomination
	for rows.Next() {
		var n domain.Nomination
		n.RoomID = roomID
		if err := rows.Scan(&n.ID, &n.Name, &n.Description); err != nil {
			return nil, err
		}
		noms = append(noms, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return noms, nil
}

func (s *Store) CreateNomination(roomID int64, name, description string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO nominations(room_id, name, description) VALUES (?, ?, ?)`, roomID, name, description)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) DeleteNomination(nominationID int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM nominations WHERE id = ?`, nominationID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) IsNominationOwner(nominationID, userID int64) (bool, error) {
	var cnt int
	err := s.db.QueryRow(`
SELECT COUNT(1)
FROM nominations nom
JOIN rooms r ON nom.room_id = r.id
WHERE nom.id = ? AND r.owner_user_id = ?
`, nominationID, userID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

func (s *Store) GetNominationRoomID(nominationID int64) (int64, error) {
	var roomID int64
	err := s.db.QueryRow(`SELECT room_id FROM nominations WHERE id = ?`, nominationID).Scan(&roomID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return roomID, nil
}

func (s *Store) CheckNominationInRoom(nominationID, roomID int64) (bool, error) {
	var cnt int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM nominations WHERE id = ? AND room_id = ?`, nominationID, roomID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

func (s *Store) GetNominationName(nominationID int64) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM nominations WHERE id = ?`, nominationID).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	return name, nil
}

// ---------- Nominees ----------

func (s *Store) CreateNominee(nominationID int64, name string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO nominees(nomination_id, name) VALUES (?, ?)`, nominationID, name)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListNominees(nominationID int64) ([]domain.Nominee, error) {
	rows, err := s.db.Query(`
SELECT
    id,
    name,
    IFNULL(media_file_id, ''),
    IFNULL(media_type, '')
FROM nominees
WHERE nomination_id = ?
ORDER BY id
`, nominationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nominees []domain.Nominee
	for rows.Next() {
		var n domain.Nominee
		n.NominationID = nominationID
		if err := rows.Scan(&n.ID, &n.Name, &n.MediaFileID, &n.MediaType); err != nil {
			return nil, err
		}
		nominees = append(nominees, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nominees, nil
}

func (s *Store) DeleteNominee(nomineeID int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM nominees WHERE id = ?`, nomineeID)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (s *Store) IsNomineeOwner(nomineeID, userID int64) (bool, error) {
	var cnt int
	err := s.db.QueryRow(`
SELECT COUNT(1)
FROM nominees n
JOIN nominations nom ON n.nomination_id = nom.id
JOIN rooms r ON nom.room_id = r.id
WHERE n.id = ? AND r.owner_user_id = ?
`, nomineeID, userID).Scan(&cnt)
	if err != nil {
		return false, err
	}
	return cnt > 0, nil
}

func (s *Store) UpdateNomineeMedia(nomineeID int64, fileID, mediaType string) error {
	_, err := s.db.Exec(`UPDATE nominees SET media_file_id = ?, media_type = ? WHERE id = ?`, fileID, mediaType, nomineeID)
	return err
}

func (s *Store) GetNomineeName(nomineeID int64) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM nominees WHERE id = ?`, nomineeID).Scan(&name)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	return name, nil
}

func (s *Store) GetNomineeNominationAndRoom(nomineeID int64) (nominationID, roomID int64, err error) {
	err = s.db.QueryRow(`
SELECT n.nomination_id, nom.room_id
FROM nominees n
JOIN nominations nom ON n.nomination_id = nom.id
WHERE n.id = ?
`, nomineeID).Scan(&nominationID, &roomID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, ErrNotFound
		}
		return 0, 0, err
	}
	return nominationID, roomID, nil
}

// ---------- Votes / Results ----------

func (s *Store) RecordVote(userHash string, nominationID, nomineeID int64, createdAt time.Time) error {
	_, err := s.db.Exec(`
INSERT INTO votes(user_hash, nomination_id, nominee_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(user_hash, nomination_id) DO UPDATE SET
    nominee_id = excluded.nominee_id,
    created_at = excluded.created_at
`, userHash, nominationID, nomineeID, createdAt)
	return err
}

func (s *Store) ResultsByNomination(nominationID int64) ([]domain.NomineeResult, error) {
	rows, err := s.db.Query(`
SELECT n.id, n.name, COUNT(v.id) as votes
FROM nominees n
LEFT JOIN votes v ON v.nominee_id = n.id
WHERE n.nomination_id = ?
GROUP BY n.id, n.name
ORDER BY votes DESC, n.id
`, nominationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []domain.NomineeResult
	for rows.Next() {
		var r domain.NomineeResult
		if err := rows.Scan(&r.ID, &r.Name, &r.Votes); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
