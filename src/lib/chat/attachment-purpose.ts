/**
 * What an attached picture was for: its style, its composition, or the picture to change.
 * #697: the composer no longer asks or sends this; the type stays for the deprecated
 * `ChatTurnOptions.attachmentPurposes` field. The server still accepts the wire field from older clients.
 */
export type AttachmentPurpose = 'style' | 'layout' | 'input';
