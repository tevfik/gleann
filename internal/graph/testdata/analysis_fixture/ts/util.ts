export function slug(text: string): string {
  return text.trim().toLowerCase().replace(/ /g, "-");
}

export function pluralize(word: string, count: number): string {
  return count === 1 ? word : word + "s";
}
