"""
Rate limiter implementations: Token Bucket, Leaky Bucket, and Sliding Window.
Used in API gateways, web scraping, and distributed systems.
"""

import time
import threading
from collections import deque
from dataclasses import dataclass, field
from typing import Optional


@dataclass
class TokenBucket:
    """Token Bucket rate limiter.
    
    Allows bursting up to `capacity` requests and refills at `rate` tokens/second.
    Thread-safe implementation using a lock.
    
    Attributes:
        capacity: Maximum tokens (burst size).
        rate: Token replenishment rate (tokens per second).
    """
    capacity: float
    rate: float
    _tokens: float = field(init=False)
    _last_refill: float = field(init=False)
    _lock: threading.Lock = field(init=False, default_factory=threading.Lock)

    def __post_init__(self):
        self._tokens = self.capacity
        self._last_refill = time.monotonic()

    def _refill(self) -> None:
        """Refill tokens based on elapsed time since last refill."""
        now = time.monotonic()
        elapsed = now - self._last_refill
        added = elapsed * self.rate
        self._tokens = min(self.capacity, self._tokens + added)
        self._last_refill = now

    def acquire(self, tokens: float = 1.0) -> bool:
        """Try to acquire `tokens` tokens. Returns True if allowed."""
        with self._lock:
            self._refill()
            if self._tokens >= tokens:
                self._tokens -= tokens
                return True
            return False

    def wait_and_acquire(self, tokens: float = 1.0, timeout: Optional[float] = None) -> bool:
        """Block until tokens are available or timeout expires."""
        deadline = time.monotonic() + timeout if timeout is not None else None
        while True:
            with self._lock:
                self._refill()
                if self._tokens >= tokens:
                    self._tokens -= tokens
                    return True
            if deadline is not None and time.monotonic() >= deadline:
                return False
            wait_time = (tokens - self._tokens) / self.rate
            sleep_time = min(wait_time, 0.01)
            if deadline is not None:
                sleep_time = min(sleep_time, deadline - time.monotonic())
            if sleep_time > 0:
                time.sleep(sleep_time)


class LeakyBucket:
    """Leaky Bucket rate limiter.
    
    Yields requests at a constant `rate`, regardless of burstiness.
    Excess requests are queued up to `capacity`.
    """
    
    def __init__(self, rate: float, capacity: int = 100):
        self.rate = rate          # requests per second (drain rate)
        self.capacity = capacity  # max queue depth
        self._queue: deque = deque()
        self._last_leak = time.monotonic()
        self._lock = threading.Lock()

    def _leak(self) -> None:
        """Drain the queue based on elapsed time."""
        now = time.monotonic()
        elapsed = now - self._last_leak
        to_drain = int(elapsed * self.rate)
        for _ in range(to_drain):
            if self._queue:
                self._queue.popleft()
        self._last_leak = now

    def enqueue(self) -> bool:
        """Try to enqueue a request. Returns True if accepted, False if full."""
        with self._lock:
            self._leak()
            if len(self._queue) < self.capacity:
                self._queue.append(time.monotonic())
                return True
            return False

    @property
    def queue_depth(self) -> int:
        with self._lock:
            self._leak()
            return len(self._queue)


class SlidingWindowCounter:
    """Sliding Window Counter rate limiter.
    
    Limits to `limit` requests per `window` seconds using a sliding time window.
    More accurate than fixed-window counters, cheaper than sliding-log.
    
    Uses a weighted count combining the previous window's tail and current window.
    """

    def __init__(self, limit: int, window: float):
        self.limit = limit
        self.window = window
        self._lock = threading.Lock()
        self._current_count = 0
        self._previous_count = 0
        self._window_start = time.monotonic()

    def _advance_window_if_needed(self) -> None:
        now = time.monotonic()
        elapsed = now - self._window_start
        if elapsed >= self.window * 2:
            # More than two windows elapsed — reset
            self._previous_count = 0
            self._current_count = 0
            self._window_start = now
        elif elapsed >= self.window:
            # Advance one window
            self._previous_count = self._current_count
            self._current_count = 0
            self._window_start += self.window

    def _estimate_count(self) -> float:
        """Weighted request count considering partial window overlap."""
        now = time.monotonic()
        elapsed_in_window = now - self._window_start
        overlap = 1.0 - (elapsed_in_window / self.window)
        return self._previous_count * overlap + self._current_count

    def allow(self) -> bool:
        """Returns True if the request is within the rate limit."""
        with self._lock:
            self._advance_window_if_needed()
            if self._estimate_count() < self.limit:
                self._current_count += 1
                return True
            return False


class DistributedRateLimiter:
    """Distributed rate limiter using Redis-compatible INCR+EXPIRE pattern.
    
    Implements a fixed-window counter in a distributed store.
    Compatible with Redis, Memcached, or any KV store with atomic increment.
    """

    def __init__(self, limit: int, window: int, store=None):
        """
        Args:
            limit: Max requests per window.
            window: Window size in seconds.
            store: A dict-like object with incr(key) and expire(key, ttl) methods.
                   Defaults to an in-memory mock.
        """
        self.limit = limit
        self.window = window
        self._store = store or _InMemoryStore()

    def allow(self, key: str) -> tuple[bool, int]:
        """
        Check if request for `key` is allowed.
        Returns (allowed: bool, remaining: int).
        """
        bucket_key = f"rl:{key}:{int(time.time()) // self.window}"
        count = self._store.incr(bucket_key)
        if count == 1:
            self._store.expire(bucket_key, self.window * 2)
        remaining = max(0, self.limit - count)
        return count <= self.limit, remaining


class _InMemoryStore:
    """Thread-safe in-memory mock of a distributed KV store for testing."""

    def __init__(self):
        self._data: dict = {}
        self._expiry: dict = {}
        self._lock = threading.Lock()

    def incr(self, key: str) -> int:
        with self._lock:
            self._evict_expired()
            self._data[key] = self._data.get(key, 0) + 1
            return self._data[key]

    def expire(self, key: str, ttl: float) -> None:
        with self._lock:
            self._expiry[key] = time.monotonic() + ttl

    def _evict_expired(self) -> None:
        now = time.monotonic()
        expired = [k for k, exp in self._expiry.items() if now > exp]
        for k in expired:
            self._data.pop(k, None)
            self._expiry.pop(k, None)
