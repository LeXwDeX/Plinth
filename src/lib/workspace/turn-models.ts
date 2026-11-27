// The models a chat turn carries: `model` for the stage's domain, `domainModels` for every other domain.
// #678: a model confirmed for one turn (转 3D without a pick) is that domain's model for this turn only. When the
// stage itself is that domain (a 3D model on the stage, 只用正面转 3D), it becomes the turn's model; it is never
// dropped for being the stage's domain. The person's own pick for the stage domain still comes first.
export function turnModels(
  domain: string,
  picks: Readonly<Record<string, string | null | undefined>>,
  oneTurn?: Readonly<Record<string, string>>,
): { model: string | undefined; domainModels: Record<string, string> } {
  const domainModels = Object.fromEntries(
    Object.entries({ ...picks, ...oneTurn }).filter(
      (entry): entry is [string, string] => entry[0] !== domain && Boolean(entry[1]),
    ),
  );
  return { model: picks[domain] || oneTurn?.[domain] || undefined, domainModels };
}
