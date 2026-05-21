use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use reqwest::{Client, StatusCode};
use serde_json::{json, Value};
use tokio::time::sleep;

use crate::cli::MatrixOptions;

#[derive(Clone)]
pub struct MatrixClient {
    homeserver: String,
    access_token: String,
    http: Client,
}

impl MatrixClient {
    pub fn from_options(options: MatrixOptions, insecure_tls: bool) -> Result<Self> {
        let http = Client::builder()
            .danger_accept_invalid_certs(insecure_tls)
            .redirect(reqwest::redirect::Policy::limited(10))
            .build()?;
        Ok(Self {
            homeserver: options.homeserver.trim_end_matches('/').to_owned(),
            access_token: options.access_token,
            http,
        })
    }

    pub async fn sync(
        &self,
        since: Option<&str>,
        timeout_ms: u64,
        timeline_limit: u32,
    ) -> Result<Value> {
        let filter = json!({
            "room": {
                "timeline": {"limit": timeline_limit},
                "state": {"lazy_load_members": true}
            },
            "presence": {"types": []}
        });
        let mut url = format!(
            "{}/_matrix/client/v3/sync?timeout={}&filter={}",
            self.homeserver,
            timeout_ms,
            urlencoding::encode(&filter.to_string())
        );
        if let Some(token) = since {
            url.push_str("&since=");
            url.push_str(&urlencoding::encode(token));
        }
        self.get_json(&url).await
    }

    pub async fn messages(&self, room_id: &str, from: &str, limit: u32) -> Result<Value> {
        let url = format!(
            "{}/_matrix/client/v3/rooms/{}/messages?dir=b&from={}&limit={}",
            self.homeserver,
            urlencoding::encode(room_id),
            urlencoding::encode(from),
            limit
        );
        self.get_json(&url).await
    }

    pub async fn joined_rooms(&self) -> Result<Vec<String>> {
        let url = format!("{}/_matrix/client/v3/joined_rooms", self.homeserver);
        let response = self.get_json(&url).await?;
        Ok(response
            .get("joined_rooms")
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
            .filter_map(Value::as_str)
            .map(ToOwned::to_owned)
            .collect())
    }

    pub async fn room_state(&self, room_id: &str) -> Result<Value> {
        let url = format!(
            "{}/_matrix/client/v3/rooms/{}/state",
            self.homeserver,
            urlencoding::encode(room_id)
        );
        self.get_json(&url).await
    }

    pub async fn download_media(&self, mxc_uri: &str) -> Result<DownloadedMedia> {
        let (server_name, media_id) = parse_mxc(mxc_uri)?;
        let url = format!(
            "{}/_matrix/client/v1/media/download/{}/{}",
            self.homeserver,
            urlencoding::encode(&server_name),
            urlencoding::encode(&media_id)
        );
        for attempt in 0..5 {
            let response = self
                .http
                .get(&url)
                .bearer_auth(&self.access_token)
                .send()
                .await
                .with_context(|| format!("download Matrix media {mxc_uri}"))?;
            if response.status() == StatusCode::TOO_MANY_REQUESTS {
                wait_for_rate_limit(response, attempt).await?;
                continue;
            }
            if !response.status().is_success() {
                return Err(anyhow!(
                    "download Matrix media {} returned HTTP {}",
                    mxc_uri,
                    response.status()
                ));
            }
            let mimetype = response
                .headers()
                .get(reqwest::header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok())
                .map(ToOwned::to_owned);
            let bytes = response.bytes().await?.to_vec();
            return Ok(DownloadedMedia { bytes, mimetype });
        }
        Err(anyhow!(
            "download Matrix media {} rate limited too often",
            mxc_uri
        ))
    }

    async fn get_json(&self, url: &str) -> Result<Value> {
        for attempt in 0..5 {
            let response = self
                .http
                .get(url)
                .bearer_auth(&self.access_token)
                .send()
                .await
                .with_context(|| format!("GET {url}"))?;
            if response.status() == StatusCode::TOO_MANY_REQUESTS {
                wait_for_rate_limit(response, attempt).await?;
                continue;
            }
            if !response.status().is_success() {
                return Err(anyhow!("GET {} returned HTTP {}", url, response.status()));
            }
            return response.json::<Value>().await.map_err(Into::into);
        }
        Err(anyhow!("GET {} rate limited too often", url))
    }
}

pub struct DownloadedMedia {
    pub bytes: Vec<u8>,
    pub mimetype: Option<String>,
}

fn parse_mxc(mxc_uri: &str) -> Result<(String, String)> {
    let rest = mxc_uri
        .strip_prefix("mxc://")
        .ok_or_else(|| anyhow!("invalid mxc URI {}", mxc_uri))?;
    let (server, media_id) = rest
        .split_once('/')
        .ok_or_else(|| anyhow!("invalid mxc URI {}", mxc_uri))?;
    if server.is_empty() || media_id.is_empty() {
        return Err(anyhow!("invalid mxc URI {}", mxc_uri));
    }
    Ok((server.to_owned(), media_id.to_owned()))
}

async fn wait_for_rate_limit(response: reqwest::Response, attempt: u32) -> Result<()> {
    let retry_header = response
        .headers()
        .get("Retry-After")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<u64>().ok())
        .map(|seconds| seconds * 1000);
    let body = response.json::<Value>().await.unwrap_or_else(|_| json!({}));
    let retry_json = body.get("retry_after_ms").and_then(Value::as_u64);
    let base_ms = retry_json.or(retry_header).unwrap_or(500);
    let jitter = 75 * u64::from(attempt + 1);
    sleep(Duration::from_millis(base_ms + jitter)).await;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_mxc_uri() -> Result<()> {
        let parsed = parse_mxc("mxc://example.org/abc123")?;
        assert_eq!(parsed, ("example.org".into(), "abc123".into()));
        Ok(())
    }

    #[test]
    fn rejects_invalid_mxc_uri() {
        assert!(parse_mxc("https://example.org/media").is_err());
    }
}
