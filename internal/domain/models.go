package domain

import "time"

type Room struct {
	ID          int64
	OwnerUserID int64
	Title       string
	Password    string
	CreatedAt   time.Time
}

type Nomination struct {
	ID          int64
	RoomID      int64
	Name        string
	Description string
}

type Nominee struct {
	ID           int64
	NominationID int64
	Name         string
	MediaFileID  string
	MediaType    string
}

type NomineeResult struct {
	ID    int64
	Name  string
	Votes int64
}
