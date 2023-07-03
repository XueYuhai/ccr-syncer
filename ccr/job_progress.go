package ccr

import (
	"encoding/json"
	"time"

	"github.com/selectdb/ccr_syncer/storage"
	log "github.com/sirupsen/logrus"
	"go.uber.org/zap"
)

// TODO: rewrite all progress by two level state machine
// first one is sync state, second one is job state

const (
	UPDATE_JOB_PROGRESS_DURATION = time.Second * 3
)

type JobState int

const (
	JobStateDoing JobState = 0
	JobStateDone  JobState = 1

	JobStateFullSync_BeginCreateSnapshot JobState = 30
	JobStateFullSync_DoneCreateSnapshot  JobState = 31
	JobStateFullSync_BeginRestore        JobState = 32
)

type SyncState int

const (
	// Database sync state machine states
	DBFullSync              SyncState = 0
	DBTablesIncrementalSync SyncState = 1
	DBSpecificTableFullSync SyncState = 2
	DBIncrementalSync       SyncState = 3

	// Table sync state machine states
	TableFullSync        SyncState = 100
	TableIncrementalSync SyncState = 101
	// Table sync sub states
	TableFullSync_
)

type JobProgress struct {
	JobName string     `json:"job_name"`
	db      storage.DB `json:"-"`

	JobState      JobState  `json:"state"`
	SyncState     SyncState `json:"sync_state"`
	CommitSeq     int64     `json:"commit_seq"`
	TransactionId int64     `json:"transaction_id"`
	Data          string    `json:"data"` // this often for binlog or snapshot info

	// Only for TablesIncrementalSync
	DbTableCommitSeqMap map[int64]int64 `json:"table_commit_seq_map"` // this often for table commit seq
}

func NewJobProgress(jobName string, db storage.DB) *JobProgress {
	return &JobProgress{
		JobName: jobName,
		db:      db,

		SyncState:     DBFullSync,
		JobState:      JobStateDone,
		CommitSeq:     0,
		TransactionId: 0,
	}
}

// create JobProgress from json data
func NewJobProgressFromJson(jobName string, db storage.DB) (*JobProgress, error) {
	// get progress from db, retry 3 times
	var err error
	var jsonData string
	for i := 0; i < 3; i++ {
		jsonData, err = db.GetProgress(jobName)
		if err != nil {
			log.Error("get job progress failed", zap.String("job", jobName), zap.Error(err))
			continue
		}
		break
	}
	if err != nil {
		return nil, err
	}

	var jobProgress JobProgress
	if err := json.Unmarshal([]byte(jsonData), &jobProgress); err != nil {
		return nil, err
	} else {
		jobProgress.db = db
		return &jobProgress, nil
	}
}

// ToJson
func (j *JobProgress) ToJson() (string, error) {
	if jsonData, err := json.Marshal(j); err != nil {
		return "", err
	} else {
		return string(jsonData), nil
	}
}

// TODO: Add api, begin/commit/abort

func (j *JobProgress) BeginCreateSnapshot() {
	j.SyncState = DBFullSync
	j.JobState = JobStateFullSync_BeginCreateSnapshot
	j.CommitSeq = 0

	j.persist()
}

func (j *JobProgress) DoneCreateSnapshot(snapshotName string) {
	j.JobState = JobStateFullSync_DoneCreateSnapshot
	j.Data = snapshotName

	j.persist()
}

func (j *JobProgress) BeginTableRestore(commitSeq int64) {
	j.JobState = JobStateFullSync_BeginRestore
	j.CommitSeq = commitSeq

	j.persist()
}

func (j *JobProgress) BeginDbRestore(DbTableCommitSeqMap map[int64]int64) {
	j.JobState = JobStateFullSync_BeginRestore
	commitSeq := int64(0)
	for _, seq := range DbTableCommitSeqMap {
		if commitSeq < seq {
			commitSeq = seq
		}
	}
	j.CommitSeq = commitSeq
	j.DbTableCommitSeqMap = DbTableCommitSeqMap

	j.persist()
}

func (j *JobProgress) DoneDbRestore(DbTableCommitSeqMap map[int64]int64) {
	j.JobState = JobStateDone

	j.persist()
}

func (j *JobProgress) NewIncrementalSync() {
	j.SyncState = TableIncrementalSync

	j.persist()
}

func (j *JobProgress) StartHandle(commitSeq int64) {
	j.JobState = JobStateDoing
	j.CommitSeq = commitSeq
	j.TransactionId = 0

	j.persist()
}

func (j *JobProgress) Done() {
	j.JobState = JobStateDone

	j.persist()
}

func (j *JobProgress) BeginTransaction(txnId int64) {
	j.TransactionId = txnId

	j.persist()
}

// write progress to db, busy loop until success
// TODO: add timeout check
func (j *JobProgress) persist() {
	log.Tracef("update job progress: %v", j)
	for {
		// Step 1: to json
		// TODO: fix to json error
		progressJson, err := j.ToJson()
		if err != nil {
			log.Error("parse job progress failed", zap.String("job", j.JobName), zap.Error(err))
			time.Sleep(UPDATE_JOB_PROGRESS_DURATION)
			continue
		}

		// Step 2: write to db
		err = j.db.UpdateProgress(j.JobName, progressJson)
		if err != nil {
			log.Error("update job progress failed", zap.String("job", j.JobName), zap.Error(err))
			time.Sleep(UPDATE_JOB_PROGRESS_DURATION)
			continue
		}

		break
	}
	log.Tracef("update job progress done: %v", j)
}
