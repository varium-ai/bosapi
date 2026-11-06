const CHARACTER_GROUPS = [
  'abcdefghjkmnpqrstuvwxyz',
  'ABCDEFGHJKMNPQRSTUVWXYZ',
  '23456789',
  '!@#$%^&*-_=+',
];
const ALL_CHARACTERS = CHARACTER_GROUPS.join('');

export function generatePassword(): string {
  const random = new Uint32Array(1);
  const randomIndex = (length: number): number => {
    // Reject the incomplete final interval to avoid modulo bias.
    const limit = Math.floor(0x100000000 / length) * length;
    do {
      crypto.getRandomValues(random);
    } while (random[0] >= limit);
    return random[0] % length;
  };

  const characters = CHARACTER_GROUPS.map(group => group[randomIndex(group.length)]);
  while (characters.length < 16) {
    characters.push(ALL_CHARACTERS[randomIndex(ALL_CHARACTERS.length)]);
  }
  for (let index = characters.length - 1; index > 0; index--) {
    const other = randomIndex(index + 1);
    [characters[index], characters[other]] = [characters[other], characters[index]];
  }
  return characters.join('');
}
