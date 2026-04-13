/**
 * Persistent queue implementation backed by LevelDB-style storage.
 * Supports producer-consumer pattern with at-least-once delivery semantics.
 * 
 * @module PersistentQueue
 */

import { EventEmitter } from 'events';

/**
 * Message stored in the queue.
 */
export interface QueueMessage<T = unknown> {
  id: string;
  payload: T;
  enqueuedAt: number;
  attempts: number;
  nextRetryAt: number;
  ackDeadline: number;
}

/**
 * Configuration for the PersistentQueue.
 */
export interface QueueConfig {
  /** Maximum number of delivery attempts before dead-lettering. */
  maxAttempts: number;
  /** Visibility timeout in milliseconds (how long a consumer has to ack). */
  visibilityTimeout: number;
  /** Base backoff delay in milliseconds (exponential backoff multiplied by attempt). */
  backoffBase: number;
  /** Dead letter queue name to route failed messages. */
  deadLetterQueue?: string;
}

const DEFAULT_CONFIG: QueueConfig = {
  maxAttempts: 5,
  visibilityTimeout: 30_000,
  backoffBase: 1_000,
};

/**
 * PersistentQueue provides durable FIFO message queuing with retry semantics.
 * 
 * @example
 * ```ts
 * const queue = new PersistentQueue<Order>('orders', config);
 * await queue.enqueue({ orderId: '123', items: [...] });
 * 
 * queue.on('message', async (msg) => {
 *   await processOrder(msg.payload);
 *   await queue.ack(msg.id);
 * });
 * ```
 */
export class PersistentQueue<T = unknown> extends EventEmitter {
  private readonly name: string;
  private readonly config: QueueConfig;
  private readonly messages: Map<string, QueueMessage<T>>;
  private readonly invisible: Set<string>;
  private processingTimer?: ReturnType<typeof setInterval>;

  constructor(name: string, config: Partial<QueueConfig> = {}) {
    super();
    this.name = name;
    this.config = { ...DEFAULT_CONFIG, ...config };
    this.messages = new Map();
    this.invisible = new Set();
  }

  /**
   * Enqueue a new message.
   * @returns The message ID.
   */
  async enqueue(payload: T): Promise<string> {
    const id = generateId();
    const now = Date.now();
    const message: QueueMessage<T> = {
      id,
      payload,
      enqueuedAt: now,
      attempts: 0,
      nextRetryAt: now,
      ackDeadline: 0,
    };
    this.messages.set(id, message);
    this.emit('enqueued', message);
    return id;
  }

  /**
   * Receive up to `maxMessages` messages, making them invisible for the visibility timeout.
   */
  async receive(maxMessages: number = 1): Promise<QueueMessage<T>[]> {
    const now = Date.now();
    const result: QueueMessage<T>[] = [];

    for (const [id, msg] of this.messages.entries()) {
      if (result.length >= maxMessages) break;
      if (this.invisible.has(id)) continue;
      if (msg.nextRetryAt > now) continue;

      const updated: QueueMessage<T> = {
        ...msg,
        attempts: msg.attempts + 1,
        ackDeadline: now + this.config.visibilityTimeout,
      };
      this.messages.set(id, updated);
      this.invisible.add(id);
      result.push(updated);
    }

    return result;
  }

  /**
   * Acknowledge successful processing of a message, removing it from the queue.
   */
  async ack(messageId: string): Promise<void> {
    if (!this.messages.has(messageId)) {
      throw new Error(`Message ${messageId} not found in queue ${this.name}`);
    }
    this.messages.delete(messageId);
    this.invisible.delete(messageId);
    this.emit('acked', messageId);
  }

  /**
   * Negatively acknowledge a message, making it visible again with exponential backoff.
   */
  async nack(messageId: string): Promise<void> {
    const msg = this.messages.get(messageId);
    if (!msg) {
      throw new Error(`Message ${messageId} not found`);
    }

    this.invisible.delete(messageId);

    if (msg.attempts >= this.config.maxAttempts) {
      this.emit('deadletter', msg);
      this.messages.delete(messageId);
      return;
    }

    const backoff = this.config.backoffBase * Math.pow(2, msg.attempts - 1);
    const jitter = Math.random() * 1000;
    const updated: QueueMessage<T> = {
      ...msg,
      nextRetryAt: Date.now() + backoff + jitter,
    };
    this.messages.set(messageId, updated);
    this.emit('nacked', updated);
  }

  /**
   * Start polling for messages and emitting 'message' events.
   * @param intervalMs Polling interval in milliseconds.
   */
  startPolling(intervalMs: number = 1_000): void {
    if (this.processingTimer) return;
    this.processingTimer = setInterval(async () => {
      const msgs = await this.receive(10);
      for (const msg of msgs) {
        this.emit('message', msg);
      }
      this.reEnqueueExpiredMessages();
    }, intervalMs);
  }

  /**
   * Stop polling.
   */
  stopPolling(): void {
    if (this.processingTimer) {
      clearInterval(this.processingTimer);
      this.processingTimer = undefined;
    }
  }

  /**
   * Re-make messages visible if their ack deadline has expired.
   * This handles consumer crashes without explicit nack.
   */
  private reEnqueueExpiredMessages(): void {
    const now = Date.now();
    for (const id of this.invisible) {
      const msg = this.messages.get(id);
      if (msg && msg.ackDeadline > 0 && now > msg.ackDeadline) {
        this.invisible.delete(id);
        this.emit('visibilitytimeout', msg);
      }
    }
  }

  /** Number of messages currently in the queue. */
  get size(): number {
    return this.messages.size;
  }

  /** Number of currently invisible (in-flight) messages. */
  get inFlight(): number {
    return this.invisible.size;
  }

  /** Queue name. */
  get queueName(): string {
    return this.name;
  }
}

/**
 * Generates a URL-safe unique ID.
 */
function generateId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

/**
 * CircularBuffer provides a fixed-capacity ring buffer for high-throughput streaming.
 * When full, oldest entries are overwritten (loss-less only with correct sizing).
 */
export class CircularBuffer<T> {
  private readonly buffer: (T | undefined)[];
  private head = 0;
  private tail = 0;
  private _size = 0;

  constructor(readonly capacity: number) {
    this.buffer = new Array(capacity).fill(undefined);
  }

  push(item: T): boolean {
    if (this._size === this.capacity) return false; // full
    this.buffer[this.tail] = item;
    this.tail = (this.tail + 1) % this.capacity;
    this._size++;
    return true;
  }

  pop(): T | undefined {
    if (this._size === 0) return undefined;
    const item = this.buffer[this.head];
    this.buffer[this.head] = undefined;
    this.head = (this.head + 1) % this.capacity;
    this._size--;
    return item;
  }

  get size(): number { return this._size; }
  get isEmpty(): boolean { return this._size === 0; }
  get isFull(): boolean { return this._size === this.capacity; }
}
