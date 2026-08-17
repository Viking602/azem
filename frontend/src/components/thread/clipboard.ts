export function shouldReadNativeClipboard(clipboardData: DataTransfer): boolean {
  return pastedImages(clipboardData).length === 0 && !clipboardData.types.includes("text/plain");
}

export function pastedImages(clipboardData: DataTransfer): File[] {
  const files = Array.from(clipboardData.files);
  const candidates = files.length > 0
    ? files
    : Array.from(clipboardData.items)
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);
  return candidates.filter((file) => file.type.startsWith("image/"));
}

export function namedClipboardImage(file: File, timestamp: Date, index: number): File {
  const extension = file.type === "image/jpeg" || file.type === "image/jpg" ? "jpg"
    : file.type === "image/gif" ? "gif"
      : file.type === "image/webp" ? "webp"
        : "png";
  const suffix = index === 0 ? "" : `-${index + 1}`;
  const name = `pasted-image-${timestamp.toISOString().replace(/[:.]/g, "-")}${suffix}.${extension}`;
  return new File([file], name, { type: file.type || `image/${extension}`, lastModified: file.lastModified });
}
