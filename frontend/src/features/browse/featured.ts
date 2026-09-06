import type { ViewerItem } from '../../core/api';

// Original, spoiler-light demo copy. Provider descriptions remain intact in details.
const demoSummaries: Record<string, string> = {
  'The Shawshank Redemption|1994': 'An innocent man enters a brutal prison. An unlikely friendship and a quiet refusal to give up become his lifeline.',
  'The Godfather|1972': 'A reluctant son is drawn into his family’s criminal empire, where loyalty carries a price and power changes everyone.',
  'The Dark Knight|2008': 'Gotham’s masked protector faces a criminal who turns chaos into a weapon—and tests how far a hero will go.',
  'The Godfather Part II|1974': 'As Michael Corleone tightens his grip on the family empire, his father’s beginnings reveal the cost of building it.',
  '12 Angry Men|1957': 'One juror questions an apparently obvious verdict. In a sweltering room, twelve strangers must confront their doubts and prejudices.',
  "Schindler's List|1993": 'In Nazi-occupied Poland, a businessman risks his fortune and his life to protect the Jewish workers in his factory.',
  'Pulp Fiction|1994': 'Two hitmen, a boxer, and a crime boss’s wife cross paths in a darkly comic tangle of bad choices and second chances.',
  'Forrest Gump|1994': 'An unassuming man finds himself at the heart of extraordinary moments, guided by a simple outlook and an enduring love.',
  'Fight Club|1999': 'An insomniac and a charismatic stranger start an underground fight club. What begins as an escape soon takes on a life of its own.',
  'Inception|2010': 'A thief who steals secrets from dreams takes on an impossible assignment: plant an idea in someone else’s mind.',
  'Breaking Bad|2008': 'A chemistry teacher facing a devastating diagnosis enters the drug trade to provide for his family. Every choice pulls him deeper.',
  'Better Call Saul|2015': 'An ambitious lawyer searches for his place in a world of shortcuts, family rivalries, and clients who play by their own rules.',
  'The Wire|2002': 'On both sides of the law, the people of Baltimore navigate institutions that reward survival more often than justice.',
  'The Sopranos|1999': 'A New Jersey mob boss seeks therapy while trying to hold together two demanding families: the one at home and the one he runs.',
  'Game of Thrones|2011': 'Rival houses fight for a kingdom while a danger beyond its borders threatens to make their ambitions meaningless.',
  'Band of Brothers|2001': 'From training camp to the front lines of World War II, the soldiers of Easy Company depend on each other to get home.',
  'Chernobyl|2019': 'After a nuclear disaster, scientists and workers race to contain the damage while confronting a system built to conceal the truth.',
  'Severance|2022': 'Office workers divide their memories between work and home. One employee begins to question what happens on the other side.',
  'The Last of Us|2023': 'A hardened survivor escorts a teenage girl across a devastated America, on a journey that could change the future.',
  'Succession|2018': 'The children of a powerful media mogul compete for control of the family empire, where affection and ambition rarely align.',
};

export function summaryFor(item: ViewerItem): string {
  const editorial = item.demo && demoSummaries[`${item.title}|${item.year}`];
  if (editorial) return editorial;
  const text = (item.synopsis ?? '').replace(/<[^>]*>/g, '').replace(/\s+/g, ' ').trim();
  if (text.length <= 240) return text;
  const sentences = text.match(/[^.!?]+[.!?]+(?:\s|$)/g);
  const first = sentences?.[0]?.trim();
  if (first && first.length >= 70 && first.length <= 240) return first;
  return text.slice(0, 237).replace(/\s+\S*$/, '') + '…';
}

export function featuredItems(items: ViewerItem[]): ViewerItem[] {
  const unique = [...new Map(items.filter((item) => item.kind !== 'episode').map((item) => [item.id, item])).values()];
  const films = unique.filter((item) => item.kind === 'film');
  const shows = unique.filter((item) => item.kind === 'series');
  // Preserve library ordering. No invented popularity scores or provider requests.
  if (!films.length || !shows.length) return unique.slice(0, 10);
  return Array.from({ length: 5 }, (_, index) => [shows[index], films[index]]).flat().filter(Boolean);
}
