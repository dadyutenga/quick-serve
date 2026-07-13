package routes

import "sync"

// siteQuotaMu serializes combined storage quota checks across KV and files
// for the same site_id (process-local soft quota enforcement).
var siteQuotaMu sync.Map // int64 siteID -> *sync.Mutex

func siteQuotaLock(siteID int64) *sync.Mutex {
	v, _ := siteQuotaMu.LoadOrStore(siteID, &sync.Mutex{})
	return v.(*sync.Mutex)
}
