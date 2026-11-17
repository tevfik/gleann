import { slug, pluralize } from "./util";

interface Shape {
  area(): number;
}

export class Square implements Shape {
  constructor(private side: number) {}

  area(): number {
    return this.side * this.side;
  }

  label(): string {
    return slug("Square " + this.side) + " " + pluralize("unit", this.side);
  }
}

export function describeAll(shapes: Square[]): string[] {
  return shapes.map((s) => s.label());
}

export function totalArea(shapes: Square[]): number {
  return shapes.reduce((acc, s) => acc + s.area(), 0);
}

export function main(): void {
  const shapes = [new Square(2), new Square(3)];
  console.log(describeAll(shapes), totalArea(shapes));
}
