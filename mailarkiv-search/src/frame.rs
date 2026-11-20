//! Frames: a 4-byte unsigned big-endian length, then that many bytes of JSON.

use std::io::{self, Read, Write};

use serde::Serialize;
use serde::de::DeserializeOwned;

/// The largest frame either side may send.
pub const MAX_FRAME: u32 = 64 * 1024 * 1024;

/// Why reading a frame stopped.
#[derive(Debug)]
pub enum ReadError {
    /// Standard input closed between frames.
    Closed,
    /// The frame broke the rules: a length of 0 or over MAX_FRAME, a body cut
    /// short, or JSON that doesn't match.
    Broken,
}

/// Reads one frame and decodes it.
pub fn read<T: DeserializeOwned>(input: &mut impl Read) -> Result<T, ReadError> {
    let mut length = [0u8; 4];
    let mut filled = 0;
    while filled < length.len() {
        match input.read(&mut length[filled..]) {
            Ok(0) if filled == 0 => return Err(ReadError::Closed),
            Ok(0) => return Err(ReadError::Broken),
            Ok(n) => filled += n,
            Err(err) if err.kind() == io::ErrorKind::Interrupted => {}
            Err(_) => return Err(ReadError::Broken),
        }
    }
    let length = u32::from_be_bytes(length);
    if length == 0 || length > MAX_FRAME {
        return Err(ReadError::Broken);
    }
    let mut body = vec![0u8; length as usize];
    input.read_exact(&mut body).map_err(|_| ReadError::Broken)?;
    serde_json::from_slice(&body).map_err(|_| ReadError::Broken)
}

/// Encodes value and writes it as one frame.
pub fn write(output: &mut impl Write, value: &impl Serialize) -> io::Result<()> {
    let body = serde_json::to_vec(value).map_err(io::Error::other)?;
    let length = u32::try_from(body.len())
        .ok()
        .filter(|&length| length <= MAX_FRAME)
        .ok_or_else(|| io::Error::other("frame over 64 MiB"))?;
    output.write_all(&length.to_be_bytes())?;
    output.write_all(&body)?;
    output.flush()
}
