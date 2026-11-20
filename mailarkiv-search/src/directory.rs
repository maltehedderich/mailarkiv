//! A directory that remembers why a write failed.
//!
//! An indexing thread that fails stops the whole `IndexWriter`. Only the call
//! that joins that thread gets its I/O error; later calls, and a join that
//! reaches a healthy thread first, get `ErrorInThread` without a cause. The
//! cause the server shows (no space, no permission) comes from here then.

use std::io::{self, BufWriter, Write};
use std::path::Path;
use std::sync::{Arc, Mutex};

use tantivy::directory::error::{
    DeleteError, LockError, OpenDirectoryError, OpenReadError, OpenWriteError,
};
use tantivy::directory::{
    AntiCallToken, Directory, DirectoryLock, FileHandle, FileSlice, Lock, MmapDirectory,
    TerminatingWrite, WatchCallback, WatchHandle, WritePtr,
};

type Failure = Arc<Mutex<Option<io::ErrorKind>>>;

/// An `MmapDirectory` that records the kind of the latest failed write.
#[derive(Clone, Debug)]
pub struct RecordingDirectory {
    inner: MmapDirectory,
    failure: Failure,
}

impl RecordingDirectory {
    pub fn open(path: &Path) -> Result<Self, OpenDirectoryError> {
        Ok(RecordingDirectory {
            inner: MmapDirectory::open(path)?,
            failure: Arc::default(),
        })
    }

    /// Returns the kind of the latest failed write and forgets it.
    pub fn take_failure(&self) -> Option<io::ErrorKind> {
        self.failure.lock().unwrap().take()
    }
}

fn record<T>(failure: &Failure, result: io::Result<T>) -> io::Result<T> {
    if let Err(err) = &result {
        *failure.lock().unwrap() = Some(err.kind());
    }
    result
}

impl Directory for RecordingDirectory {
    fn get_file_handle(&self, path: &Path) -> Result<Arc<dyn FileHandle>, OpenReadError> {
        self.inner.get_file_handle(path)
    }

    fn open_read(&self, path: &Path) -> Result<FileSlice, OpenReadError> {
        self.inner.open_read(path)
    }

    fn delete(&self, path: &Path) -> Result<(), DeleteError> {
        self.inner.delete(path)
    }

    fn exists(&self, path: &Path) -> Result<bool, OpenReadError> {
        self.inner.exists(path)
    }

    fn open_write(&self, path: &Path) -> Result<WritePtr, OpenWriteError> {
        let writer = self.inner.open_write(path).inspect_err(|err| {
            if let OpenWriteError::IoError { io_error, .. } = err {
                *self.failure.lock().unwrap() = Some(io_error.kind());
            }
        })?;
        // A new writer's buffer is empty, so taking it apart writes nothing.
        let file = writer
            .into_inner()
            .map_err(|err| OpenWriteError::wrap_io_error(err.into_error(), path.to_path_buf()))?;
        Ok(BufWriter::new(Box::new(RecordingWrite {
            inner: file,
            failure: Arc::clone(&self.failure),
        })))
    }

    fn atomic_read(&self, path: &Path) -> Result<Vec<u8>, OpenReadError> {
        self.inner.atomic_read(path)
    }

    fn atomic_write(&self, path: &Path, data: &[u8]) -> io::Result<()> {
        record(&self.failure, self.inner.atomic_write(path, data))
    }

    fn sync_directory(&self) -> io::Result<()> {
        record(&self.failure, self.inner.sync_directory())
    }

    // MmapDirectory locks with flock. The trait's default would use lock
    // files instead.
    fn acquire_lock(&self, lock: &Lock) -> Result<DirectoryLock, LockError> {
        self.inner.acquire_lock(lock)
    }

    fn watch(&self, watch_callback: WatchCallback) -> tantivy::Result<WatchHandle> {
        self.inner.watch(watch_callback)
    }
}

struct RecordingWrite {
    inner: Box<dyn TerminatingWrite + Send + Sync>,
    failure: Failure,
}

impl Write for RecordingWrite {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        record(&self.failure, self.inner.write(buf))
    }

    fn flush(&mut self) -> io::Result<()> {
        record(&self.failure, self.inner.flush())
    }
}

impl TerminatingWrite for RecordingWrite {
    fn terminate_ref(&mut self, token: AntiCallToken) -> io::Result<()> {
        record(&self.failure, self.inner.terminate_ref(token))
    }
}
