package pogodata

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed fallback/*.json
var fallbackFS embed.FS

// TrainerClass holds a Pokémon game trainer class for the avatar picker.
type TrainerClass struct {
	Slug      string `json:"slug"`
	Label     string `json:"label"`
	SpriteURL string `json:"sprite_url"`
	Group     string `json:"group,omitempty"`
}

const showdownBase = "https://play.pokemonshowdown.com/sprites/trainers/"

var showdownSources = map[string]string{}

func init() {
	for _, tc := range showdownModernClasses {
		showdownSources[tc.Slug] = tc.SpriteURL
	}
	for _, slice := range showdownNewSlices {
		for _, tc := range slice {
			showdownSources[tc.Slug] = showdownBase + tc.Slug + ".png"
		}
	}
}

var showdownModernClasses = []TrainerClass{
	{Slug: "youngster",    Label: "Youngster",    SpriteURL: showdownBase + "youngster.png"},
	{Slug: "lass",         Label: "Lass",          SpriteURL: showdownBase + "lass.png"},
	{Slug: "bug-catcher",  Label: "Bug Catcher",   SpriteURL: showdownBase + "bugcatcher.png"},
	{Slug: "biker",        Label: "Biker",          SpriteURL: showdownBase + "biker.png"},
	{Slug: "blackbelt",    Label: "Black Belt",     SpriteURL: showdownBase + "blackbelt.png"},
	{Slug: "hiker",        Label: "Hiker",          SpriteURL: showdownBase + "hiker.png"},
	{Slug: "fisherman",    Label: "Fisherman",      SpriteURL: showdownBase + "fisherman.png"},
	{Slug: "sailor",       Label: "Sailor",         SpriteURL: showdownBase + "sailor.png"},
	{Slug: "firebreather", Label: "Fire Breather",  SpriteURL: showdownBase + "firebreather.png"},
	{Slug: "birdkeeper",   Label: "Bird Keeper",    SpriteURL: showdownBase + "birdkeeper.png"},
	{Slug: "juggler",      Label: "Juggler",        SpriteURL: showdownBase + "juggler.png"},
	{Slug: "gambler",      Label: "Gambler",        SpriteURL: showdownBase + "gambler.png"},
	{Slug: "burglar",      Label: "Burglar",        SpriteURL: showdownBase + "burglar.png"},
	{Slug: "beauty",       Label: "Beauty",         SpriteURL: showdownBase + "beauty.png"},
	{Slug: "picnicker",    Label: "Picnicker",      SpriteURL: showdownBase + "picnicker.png"},
	{Slug: "camper",       Label: "Camper",         SpriteURL: showdownBase + "camper.png"},
	{Slug: "swimmer",      Label: "Swimmer",        SpriteURL: showdownBase + "swimmer.png"},
	{Slug: "battlegirl",   Label: "Battle Girl",    SpriteURL: showdownBase + "battlegirl.png"},
	{Slug: "dancer",       Label: "Dancer",         SpriteURL: showdownBase + "dancer.png"},
	{Slug: "psychic",      Label: "Psychic",        SpriteURL: showdownBase + "psychic.png"},
	{Slug: "ace-trainer",  Label: "Ace Trainer",    SpriteURL: showdownBase + "acetrainer.png"},
	{Slug: "dragontamer",  Label: "Dragon Tamer",   SpriteURL: showdownBase + "dragontamer.png"},
	{Slug: "pokemaniac",   Label: "Pokemaniac",     SpriteURL: showdownBase + "pokemaniac.png"},
	{Slug: "super-nerd",   Label: "Super Nerd",     SpriteURL: showdownBase + "supernerd.png"},
	{Slug: "gentleman",    Label: "Gentleman",      SpriteURL: showdownBase + "gentleman.png"},
	{Slug: "medium",       Label: "Medium",         SpriteURL: showdownBase + "medium.png"},
	{Slug: "worker",       Label: "Worker",         SpriteURL: showdownBase + "worker.png"},

	// -- Villains & Team Bosses --
	{Slug: "giovanni",  Label: "Giovanni",        SpriteURL: showdownBase + "giovanni.png"},
	{Slug: "cyrus",     Label: "Cyrus",            SpriteURL: showdownBase + "cyrus.png"},
	{Slug: "ghetsis",   Label: "Ghetsis",          SpriteURL: showdownBase + "ghetsis.png"},
	{Slug: "lysandre",  Label: "Lysandre",         SpriteURL: showdownBase + "lysandre.png"},
	{Slug: "lusamine",  Label: "Lusamine",         SpriteURL: showdownBase + "lusamine.png"},
	{Slug: "guzma",     Label: "Guzma",            SpriteURL: showdownBase + "guzma.png"},
	{Slug: "rose",      Label: "Chairman Rose",    SpriteURL: showdownBase + "rose.png"},
	{Slug: "volo",      Label: "Volo",             SpriteURL: showdownBase + "volo.png"},

	// -- Rivals --
	{Slug: "blue",      Label: "Blue",             SpriteURL: showdownBase + "blue.png"},
	{Slug: "silver",    Label: "Silver",           SpriteURL: showdownBase + "silver.png"},
	{Slug: "wally",     Label: "Wally",            SpriteURL: showdownBase + "wally.png"},
	{Slug: "barry",     Label: "Barry",            SpriteURL: showdownBase + "barry.png"},
	{Slug: "cheren",    Label: "Cheren",           SpriteURL: showdownBase + "cheren.png"},
	{Slug: "bianca",    Label: "Bianca",           SpriteURL: showdownBase + "bianca.png"},
	{Slug: "hugh",      Label: "Hugh",             SpriteURL: showdownBase + "hugh.png"},
	{Slug: "calem",     Label: "Calem",            SpriteURL: showdownBase + "calem.png"},
	{Slug: "serena",    Label: "Serena",           SpriteURL: showdownBase + "serena.png"},
	{Slug: "hop",       Label: "Hop",              SpriteURL: showdownBase + "hop.png"},
	{Slug: "bede",      Label: "Bede",             SpriteURL: showdownBase + "bede.png"},
	{Slug: "marnie",    Label: "Marnie",           SpriteURL: showdownBase + "marnie.png"},
	{Slug: "kieran",    Label: "Kieran",           SpriteURL: showdownBase + "kieran.png"},

	// -- Named Side Characters --
	{Slug: "n",         Label: "N",                SpriteURL: showdownBase + "n.png"},
	{Slug: "colress",   Label: "Colress",          SpriteURL: showdownBase + "colress.png"},
	{Slug: "gladion",   Label: "Gladion",          SpriteURL: showdownBase + "gladion.png"},
	{Slug: "lillie",    Label: "Lillie",           SpriteURL: showdownBase + "lillie.png"},
	{Slug: "hau",       Label: "Hau",              SpriteURL: showdownBase + "hau.png"},
	{Slug: "plumeria",  Label: "Plumeria",         SpriteURL: showdownBase + "plumeria.png"},
	{Slug: "sonia",     Label: "Sonia",            SpriteURL: showdownBase + "sonia.png"},
	{Slug: "penny",     Label: "Penny",            SpriteURL: showdownBase + "penny.png"},

	// -- Champions & Elite Four --
	{Slug: "cynthia",   Label: "Cynthia",          SpriteURL: showdownBase + "cynthia.png"},
	{Slug: "diantha",   Label: "Diantha",          SpriteURL: showdownBase + "diantha.png"},
	{Slug: "iris",      Label: "Iris",             SpriteURL: showdownBase + "iris.png"},
	{Slug: "alder",     Label: "Alder",            SpriteURL: showdownBase + "alder.png"},
	{Slug: "leon",      Label: "Leon",             SpriteURL: showdownBase + "leon.png"},
	{Slug: "geeta",     Label: "Geeta",            SpriteURL: showdownBase + "geeta.png"},
	{Slug: "steven",    Label: "Steven",           SpriteURL: showdownBase + "steven.png"},

	// -- Gym Leaders: Kanto --
	{Slug: "brock",     Label: "Brock",            SpriteURL: showdownBase + "brock.png"},
	{Slug: "misty",     Label: "Misty",            SpriteURL: showdownBase + "misty.png"},
	{Slug: "lt-surge",  Label: "Lt. Surge",        SpriteURL: showdownBase + "ltsurge.png"},
	{Slug: "erika",     Label: "Erika",            SpriteURL: showdownBase + "erika.png"},
	{Slug: "koga",      Label: "Koga",             SpriteURL: showdownBase + "koga.png"},
	{Slug: "sabrina",   Label: "Sabrina",          SpriteURL: showdownBase + "sabrina.png"},
	{Slug: "blaine",    Label: "Blaine",           SpriteURL: showdownBase + "blaine.png"},

	// -- Gym Leaders: Johto --
	{Slug: "falkner",   Label: "Falkner",          SpriteURL: showdownBase + "falkner.png"},
	{Slug: "bugsy",     Label: "Bugsy",            SpriteURL: showdownBase + "bugsy.png"},
	{Slug: "whitney",   Label: "Whitney",          SpriteURL: showdownBase + "whitney.png"},
	{Slug: "morty",     Label: "Morty",            SpriteURL: showdownBase + "morty.png"},
	{Slug: "jasmine",   Label: "Jasmine",          SpriteURL: showdownBase + "jasmine.png"},
	{Slug: "pryce",     Label: "Pryce",            SpriteURL: showdownBase + "pryce.png"},
	{Slug: "clair",     Label: "Clair",            SpriteURL: showdownBase + "clair.png"},

	// -- Scientists, Professors & Researchers --
	{Slug: "scientist", Label: "Scientist",        SpriteURL: showdownBase + "scientist.png"},
	{Slug: "doctor",    Label: "Doctor",           SpriteURL: showdownBase + "doctor.png"},
	{Slug: "nurse",     Label: "Nurse",            SpriteURL: showdownBase + "nurse.png"},
	{Slug: "elm",       Label: "Prof. Elm",        SpriteURL: showdownBase + "elm.png"},
	{Slug: "birch",     Label: "Prof. Birch",      SpriteURL: showdownBase + "birch.png"},
	{Slug: "rowan",     Label: "Prof. Rowan",      SpriteURL: showdownBase + "rowan.png"},
	{Slug: "sycamore",  Label: "Prof. Sycamore",   SpriteURL: showdownBase + "sycamore.png"},
	{Slug: "kukui",     Label: "Prof. Kukui",      SpriteURL: showdownBase + "kukui.png"},
	{Slug: "burnet",    Label: "Prof. Burnet",     SpriteURL: showdownBase + "burnet.png"},
	{Slug: "juniper",   Label: "Prof. Juniper",    SpriteURL: showdownBase + "juniper.png"},
	{Slug: "magnolia",  Label: "Prof. Magnolia",   SpriteURL: showdownBase + "magnolia.png"},
	{Slug: "laventon",  Label: "Prof. Laventon",   SpriteURL: showdownBase + "laventon.png"},
	{Slug: "turo",      Label: "Prof. Turo",       SpriteURL: showdownBase + "turo.png"},
	{Slug: "sada",      Label: "Prof. Sada",       SpriteURL: showdownBase + "sada.png"},
	{Slug: "willow",    Label: "Prof. Willow",     SpriteURL: showdownBase + "willow.png"},
	{Slug: "faba",      Label: "Faba",             SpriteURL: showdownBase + "faba.png"},
	{Slug: "molayne",   Label: "Molayne",          SpriteURL: showdownBase + "molayne.png"},

	// -- Playable Characters --
	{Slug: "kris",      Label: "Kris",             SpriteURL: showdownBase + "kris.png"},
	{Slug: "ethan",     Label: "Ethan",            SpriteURL: showdownBase + "ethan.png"},
	{Slug: "lyra",      Label: "Lyra",             SpriteURL: showdownBase + "lyra.png"},
	{Slug: "hilbert",   Label: "Hilbert",          SpriteURL: showdownBase + "hilbert.png"},
	{Slug: "hilda",     Label: "Hilda",            SpriteURL: showdownBase + "hilda.png"},
	{Slug: "dawn",      Label: "Dawn",             SpriteURL: showdownBase + "dawn.png"},
	{Slug: "lucas",     Label: "Lucas",            SpriteURL: showdownBase + "lucas.png"},
	{Slug: "nate",      Label: "Nate",             SpriteURL: showdownBase + "nate.png"},
	{Slug: "rosa",      Label: "Rosa",             SpriteURL: showdownBase + "rosa.png"},
	{Slug: "elio",      Label: "Elio",             SpriteURL: showdownBase + "elio.png"},
	{Slug: "selene",    Label: "Selene",           SpriteURL: showdownBase + "selene.png"},
	{Slug: "victor",    Label: "Victor",           SpriteURL: showdownBase + "victor.png"},
	{Slug: "gloria",    Label: "Gloria",           SpriteURL: showdownBase + "gloria.png"},
	{Slug: "florian-s",        Label: "Florian",              SpriteURL: showdownBase + "florian-s.png"},
	{Slug: "florian-bb",       Label: "Florian (Blueberry)",  SpriteURL: showdownBase + "florian-bb.png"},
	{Slug: "florian-festival", Label: "Florian (Festival)",   SpriteURL: showdownBase + "florian-festival.png"},
	{Slug: "juliana-s",        Label: "Juliana",              SpriteURL: showdownBase + "juliana-s.png"},
	{Slug: "juliana-bb",       Label: "Juliana (Blueberry)",  SpriteURL: showdownBase + "juliana-bb.png"},
	{Slug: "juliana-festival", Label: "Juliana (Festival)",   SpriteURL: showdownBase + "juliana-festival.png"},
	{Slug: "arven-s",   Label: "Arven (S)",        SpriteURL: showdownBase + "arven-s.png"},
	{Slug: "arven-v",   Label: "Arven (V)",        SpriteURL: showdownBase + "arven-v.png"},

	// -- Elite Four: Sinnoh --
	{Slug: "aaron",     Label: "Aaron",            SpriteURL: showdownBase + "aaron.png"},
	{Slug: "bertha",    Label: "Bertha",           SpriteURL: showdownBase + "bertha.png"},
	{Slug: "flint",     Label: "Flint",            SpriteURL: showdownBase + "flint.png"},
	{Slug: "lucian",    Label: "Lucian",           SpriteURL: showdownBase + "lucian.png"},
	{Slug: "volkner",   Label: "Volkner",          SpriteURL: showdownBase + "volkner.png"},

	// -- Elite Four: Kalos --
	{Slug: "malva",     Label: "Malva",            SpriteURL: showdownBase + "malva.png"},
	{Slug: "wikstrom",  Label: "Wikstrom",         SpriteURL: showdownBase + "wikstrom.png"},
	{Slug: "siebold",   Label: "Siebold",          SpriteURL: showdownBase + "siebold.png"},
	{Slug: "drasna",    Label: "Drasna",           SpriteURL: showdownBase + "drasna.png"},

	// -- Gym Leaders: Galar --
	{Slug: "nessa",     Label: "Nessa",            SpriteURL: showdownBase + "nessa.png"},
	{Slug: "kabu",      Label: "Kabu",             SpriteURL: showdownBase + "kabu.png"},
	{Slug: "bea",       Label: "Bea",              SpriteURL: showdownBase + "bea.png"},
	{Slug: "allister",  Label: "Allister",         SpriteURL: showdownBase + "allister.png"},
	{Slug: "opal",      Label: "Opal",             SpriteURL: showdownBase + "opal.png"},
	{Slug: "gordie",    Label: "Gordie",           SpriteURL: showdownBase + "gordie.png"},
	{Slug: "melony",    Label: "Melony",           SpriteURL: showdownBase + "melony.png"},
	{Slug: "piers",     Label: "Piers",            SpriteURL: showdownBase + "piers.png"},
	{Slug: "raihan",    Label: "Raihan",           SpriteURL: showdownBase + "raihan.png"},

	// -- Gym Leaders: Paldea --
	{Slug: "brassius",  Label: "Brassius",         SpriteURL: showdownBase + "brassius.png"},
	{Slug: "iono",      Label: "Iono",             SpriteURL: showdownBase + "iono.png"},
	{Slug: "larry",     Label: "Larry",            SpriteURL: showdownBase + "larry.png"},
	{Slug: "ryme",      Label: "Ryme",             SpriteURL: showdownBase + "ryme.png"},
	{Slug: "tulip",     Label: "Tulip",            SpriteURL: showdownBase + "tulip.png"},
	{Slug: "grusha",    Label: "Grusha",           SpriteURL: showdownBase + "grusha.png"},
	{Slug: "hassel",    Label: "Hassel",           SpriteURL: showdownBase + "hassel.png"},
	{Slug: "poppy",     Label: "Poppy",            SpriteURL: showdownBase + "poppy.png"},
}

// dreamstoneTrainerClasses are trainer front sprites from Pokémon Dreamstone Mysteries
// (github.com/dsmyst/dreamstone-mysteries), an open-source pokeemerald ROM hack.
// Sprites are stored locally under static/sprites/dreamstone/ and served as static files.
var dreamstoneTrainerClasses = []TrainerClass{
	{Slug: "ds-aqua_admin_f",         Label: "Aqua Admin F (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_admin_f.png"},
	{Slug: "ds-aqua_admin_m",         Label: "Aqua Admin M (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_admin_m.png"},
	{Slug: "ds-aqua_grunt_f",         Label: "Aqua Grunt F (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_grunt_f.png"},
	{Slug: "ds-aqua_grunt_m",         Label: "Aqua Grunt M (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_grunt_m.png"},
	{Slug: "ds-aqua_leader_archie",   Label: "Aqua Leader Archie (DS)",   SpriteURL: "/static/sprites/dreamstone/aqua_leader_archie.png"},
	{Slug: "ds-arena_tycoon_greta",   Label: "Arena Tycoon Greta (DS)",   SpriteURL: "/static/sprites/dreamstone/arena_tycoon_greta.png"},
	{Slug: "ds-aroma_lady",           Label: "Aroma Lady (DS)",           SpriteURL: "/static/sprites/dreamstone/aroma_lady.png"},
	{Slug: "ds-battle_girl",          Label: "Battle Girl (DS)",          SpriteURL: "/static/sprites/dreamstone/battle_girl.png"},
	{Slug: "ds-beauty",               Label: "Beauty (DS)",               SpriteURL: "/static/sprites/dreamstone/beauty.png"},
	{Slug: "ds-bird_keeper",          Label: "Bird Keeper (DS)",          SpriteURL: "/static/sprites/dreamstone/bird_keeper.png"},
	{Slug: "ds-black_belt",           Label: "Black Belt (DS)",           SpriteURL: "/static/sprites/dreamstone/black_belt.png"},
	{Slug: "ds-brendan",              Label: "Brendan (DS)",              SpriteURL: "/static/sprites/dreamstone/brendan.png"},
	{Slug: "ds-brendan_oras",         Label: "Brendan ORAS (DS)",         SpriteURL: "/static/sprites/dreamstone/brendan_oras.png"},
	{Slug: "ds-brendan_rs",           Label: "Brendan RS (DS)",           SpriteURL: "/static/sprites/dreamstone/brendan_rs.png"},
	{Slug: "ds-bug_catcher",          Label: "Bug Catcher (DS)",          SpriteURL: "/static/sprites/dreamstone/bug_catcher.png"},
	{Slug: "ds-bug_maniac",           Label: "Bug Maniac (DS)",           SpriteURL: "/static/sprites/dreamstone/bug_maniac.png"},
	{Slug: "ds-camper",               Label: "Camper (DS)",               SpriteURL: "/static/sprites/dreamstone/camper.png"},
	{Slug: "ds-champion_wallace",     Label: "Champion Wallace (DS)",     SpriteURL: "/static/sprites/dreamstone/champion_wallace.png"},
	{Slug: "ds-collector",            Label: "Collector (DS)",            SpriteURL: "/static/sprites/dreamstone/collector.png"},
	{Slug: "ds-cooltrainer_f",        Label: "Cool Trainer F (DS)",       SpriteURL: "/static/sprites/dreamstone/cooltrainer_f.png"},
	{Slug: "ds-cooltrainer_m",        Label: "Cool Trainer M (DS)",       SpriteURL: "/static/sprites/dreamstone/cooltrainer_m.png"},
	{Slug: "ds-cycling_triathlete_f", Label: "Cycling Triathlete F (DS)", SpriteURL: "/static/sprites/dreamstone/cycling_triathlete_f.png"},
	{Slug: "ds-cycling_triathlete_m", Label: "Cycling Triathlete M (DS)", SpriteURL: "/static/sprites/dreamstone/cycling_triathlete_m.png"},
	{Slug: "ds-dome_ace_tucker",      Label: "Dome Ace Tucker (DS)",      SpriteURL: "/static/sprites/dreamstone/dome_ace_tucker.png"},
	{Slug: "ds-dragon_tamer",         Label: "Dragon Tamer (DS)",         SpriteURL: "/static/sprites/dreamstone/dragon_tamer.png"},
	{Slug: "ds-elite_four_drake",     Label: "Elite Four Drake (DS)",     SpriteURL: "/static/sprites/dreamstone/elite_four_drake.png"},
	{Slug: "ds-elite_four_glacia",    Label: "Elite Four Glacia (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_glacia.png"},
	{Slug: "ds-elite_four_phoebe",    Label: "Elite Four Phoebe (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_phoebe.png"},
	{Slug: "ds-elite_four_sidney",    Label: "Elite Four Sidney (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_sidney.png"},
	{Slug: "ds-expert_f",             Label: "Expert F (DS)",             SpriteURL: "/static/sprites/dreamstone/expert_f.png"},
	{Slug: "ds-expert_m",             Label: "Expert M (DS)",             SpriteURL: "/static/sprites/dreamstone/expert_m.png"},
	{Slug: "ds-factory_head_noland",  Label: "Factory Head Noland (DS)",  SpriteURL: "/static/sprites/dreamstone/factory_head_noland.png"},
	{Slug: "ds-fisherman",            Label: "Fisherman (DS)",            SpriteURL: "/static/sprites/dreamstone/fisherman.png"},
	{Slug: "ds-gentleman",            Label: "Gentleman (DS)",            SpriteURL: "/static/sprites/dreamstone/gentleman.png"},
	{Slug: "ds-guitarist",            Label: "Guitarist (DS)",            SpriteURL: "/static/sprites/dreamstone/guitarist.png"},
	{Slug: "ds-hex_maniac",           Label: "Hex Maniac (DS)",           SpriteURL: "/static/sprites/dreamstone/hex_maniac.png"},
	{Slug: "ds-hiker",                Label: "Hiker (DS)",                SpriteURL: "/static/sprites/dreamstone/hiker.png"},
	{Slug: "ds-interviewer",          Label: "Interviewer (DS)",          SpriteURL: "/static/sprites/dreamstone/interviewer.png"},
	{Slug: "ds-kindler",              Label: "Kindler (DS)",              SpriteURL: "/static/sprites/dreamstone/kindler.png"},
	{Slug: "ds-lady",                 Label: "Lady (DS)",                 SpriteURL: "/static/sprites/dreamstone/lady.png"},
	{Slug: "ds-lass",                 Label: "Lass (DS)",                 SpriteURL: "/static/sprites/dreamstone/lass.png"},
	{Slug: "ds-leader_brawly",        Label: "Leader Brawly (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_brawly.png"},
	{Slug: "ds-leader_flannery",      Label: "Leader Flannery (DS)",      SpriteURL: "/static/sprites/dreamstone/leader_flannery.png"},
	{Slug: "ds-leader_juan",          Label: "Leader Juan (DS)",          SpriteURL: "/static/sprites/dreamstone/leader_juan.png"},
	{Slug: "ds-leader_norman",        Label: "Leader Norman (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_norman.png"},
	{Slug: "ds-leader_roxanne",       Label: "Leader Roxanne (DS)",       SpriteURL: "/static/sprites/dreamstone/leader_roxanne.png"},
	{Slug: "ds-leader_tate_and_liza", Label: "Leader Tate & Liza (DS)",   SpriteURL: "/static/sprites/dreamstone/leader_tate_and_liza.png"},
	{Slug: "ds-leader_wattson",       Label: "Leader Wattson (DS)",       SpriteURL: "/static/sprites/dreamstone/leader_wattson.png"},
	{Slug: "ds-leader_winona",        Label: "Leader Winona (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_winona.png"},
	{Slug: "ds-leaf",                 Label: "Leaf (DS)",                 SpriteURL: "/static/sprites/dreamstone/leaf.png"},
	{Slug: "ds-magma_admin",          Label: "Magma Admin (DS)",          SpriteURL: "/static/sprites/dreamstone/magma_admin.png"},
	{Slug: "ds-magma_grunt_f",        Label: "Magma Grunt F (DS)",        SpriteURL: "/static/sprites/dreamstone/magma_grunt_f.png"},
	{Slug: "ds-magma_grunt_m",        Label: "Magma Grunt M (DS)",        SpriteURL: "/static/sprites/dreamstone/magma_grunt_m.png"},
	{Slug: "ds-magma_leader_maxie",   Label: "Magma Leader Maxie (DS)",   SpriteURL: "/static/sprites/dreamstone/magma_leader_maxie.png"},
	{Slug: "ds-may",                  Label: "May (DS)",                  SpriteURL: "/static/sprites/dreamstone/may.png"},
	{Slug: "ds-may_oras",             Label: "May ORAS (DS)",             SpriteURL: "/static/sprites/dreamstone/may_oras.png"},
	{Slug: "ds-may_rs",               Label: "May RS (DS)",               SpriteURL: "/static/sprites/dreamstone/may_rs.png"},
	{Slug: "ds-ninja_boy",            Label: "Ninja Boy (DS)",            SpriteURL: "/static/sprites/dreamstone/ninja_boy.png"},
	{Slug: "ds-old_couple",           Label: "Old Couple (DS)",           SpriteURL: "/static/sprites/dreamstone/old_couple.png"},
	{Slug: "ds-palace_maven_spenser", Label: "Palace Maven Spenser (DS)", SpriteURL: "/static/sprites/dreamstone/palace_maven_spenser.png"},
	{Slug: "ds-parasol_lady",         Label: "Parasol Lady (DS)",         SpriteURL: "/static/sprites/dreamstone/parasol_lady.png"},
	{Slug: "ds-picnicker",            Label: "Picnicker (DS)",            SpriteURL: "/static/sprites/dreamstone/picnicker.png"},
	{Slug: "ds-pike_queen_lucy",      Label: "Pike Queen Lucy (DS)",      SpriteURL: "/static/sprites/dreamstone/pike_queen_lucy.png"},
	{Slug: "ds-pokefan_f",            Label: "Pokefan F (DS)",            SpriteURL: "/static/sprites/dreamstone/pokefan_f.png"},
	{Slug: "ds-pokefan_m",            Label: "Pokefan M (DS)",            SpriteURL: "/static/sprites/dreamstone/pokefan_m.png"},
	{Slug: "ds-pokemaniac",           Label: "Pokemaniac (DS)",           SpriteURL: "/static/sprites/dreamstone/pokemaniac.png"},
	{Slug: "ds-pokemon_breeder_f",    Label: "Pokemon Breeder F (DS)",    SpriteURL: "/static/sprites/dreamstone/pokemon_breeder_f.png"},
	{Slug: "ds-pokemon_breeder_m",    Label: "Pokemon Breeder M (DS)",    SpriteURL: "/static/sprites/dreamstone/pokemon_breeder_m.png"},
	{Slug: "ds-pokemon_ranger_f",     Label: "Pokemon Ranger F (DS)",     SpriteURL: "/static/sprites/dreamstone/pokemon_ranger_f.png"},
	{Slug: "ds-pokemon_ranger_m",     Label: "Pokemon Ranger M (DS)",     SpriteURL: "/static/sprites/dreamstone/pokemon_ranger_m.png"},
	{Slug: "ds-psychic_f",            Label: "Psychic F (DS)",            SpriteURL: "/static/sprites/dreamstone/psychic_f.png"},
	{Slug: "ds-psychic_m",            Label: "Psychic M (DS)",            SpriteURL: "/static/sprites/dreamstone/psychic_m.png"},
	{Slug: "ds-pyramid_king_brandon", Label: "Pyramid King Brandon (DS)", SpriteURL: "/static/sprites/dreamstone/pyramid_king_brandon.png"},
	{Slug: "ds-red",                  Label: "Red (DS)",                  SpriteURL: "/static/sprites/dreamstone/red.png"},
	{Slug: "ds-rich_boy",             Label: "Rich Boy (DS)",             SpriteURL: "/static/sprites/dreamstone/rich_boy.png"},
	{Slug: "ds-ruin_maniac",          Label: "Ruin Maniac (DS)",          SpriteURL: "/static/sprites/dreamstone/ruin_maniac.png"},
	{Slug: "ds-running_triathlete_f", Label: "Running Triathlete F (DS)", SpriteURL: "/static/sprites/dreamstone/running_triathlete_f.png"},
	{Slug: "ds-running_triathlete_m", Label: "Running Triathlete M (DS)", SpriteURL: "/static/sprites/dreamstone/running_triathlete_m.png"},
	{Slug: "ds-sailor",               Label: "Sailor (DS)",               SpriteURL: "/static/sprites/dreamstone/sailor.png"},
	{Slug: "ds-salon_maiden_anabel",  Label: "Salon Maiden Anabel (DS)",  SpriteURL: "/static/sprites/dreamstone/salon_maiden_anabel.png"},
	{Slug: "ds-school_kid_f",         Label: "School Kid F (DS)",         SpriteURL: "/static/sprites/dreamstone/school_kid_f.png"},
	{Slug: "ds-school_kid_m",         Label: "School Kid M (DS)",         SpriteURL: "/static/sprites/dreamstone/school_kid_m.png"},
	{Slug: "ds-sis_and_bro",          Label: "Sis & Bro (DS)",            SpriteURL: "/static/sprites/dreamstone/sis_and_bro.png"},
	{Slug: "ds-somber_grunt_f",       Label: "Somber Grunt F (DS)",       SpriteURL: "/static/sprites/dreamstone/somber_grunt_f.png"},
	{Slug: "ds-sr_and_jr",            Label: "Sr. & Jr. (DS)",            SpriteURL: "/static/sprites/dreamstone/sr_and_jr.png"},
	{Slug: "ds-steven",               Label: "Steven (DS)",               SpriteURL: "/static/sprites/dreamstone/steven.png"},
	{Slug: "ds-swimmer_f",            Label: "Swimmer F (DS)",            SpriteURL: "/static/sprites/dreamstone/swimmer_f.png"},
	{Slug: "ds-swimmer_m",            Label: "Swimmer M (DS)",            SpriteURL: "/static/sprites/dreamstone/swimmer_m.png"},
	{Slug: "ds-swimming_triathlete_f",Label: "Swimming Triathlete F (DS)",SpriteURL: "/static/sprites/dreamstone/swimming_triathlete_f.png"},
	{Slug: "ds-swimming_triathlete_m",Label: "Swimming Triathlete M (DS)",SpriteURL: "/static/sprites/dreamstone/swimming_triathlete_m.png"},
	{Slug: "ds-tuber_f",              Label: "Tuber F (DS)",              SpriteURL: "/static/sprites/dreamstone/tuber_f.png"},
	{Slug: "ds-tuber_m",              Label: "Tuber M (DS)",              SpriteURL: "/static/sprites/dreamstone/tuber_m.png"},
	{Slug: "ds-twins",                Label: "Twins (DS)",                SpriteURL: "/static/sprites/dreamstone/twins.png"},
	{Slug: "ds-wally",                Label: "Wally (DS)",                SpriteURL: "/static/sprites/dreamstone/wally.png"},
	{Slug: "ds-young_couple",         Label: "Young Couple (DS)",         SpriteURL: "/static/sprites/dreamstone/young_couple.png"},
	{Slug: "ds-youngster",            Label: "Youngster (DS)",            SpriteURL: "/static/sprites/dreamstone/youngster.png"},
}

// pexTrainerClasses are FRLG-style trainer front sprites from pokeemerald-expansion
// (github.com/rh-hideout/pokeemerald-expansion), a community pokeemerald ROM hack.
// Sprites are stored locally under static/sprites/pex/ and served as static files.
// Attribution: Rom Hacking Hideout (RHH).
var pexTrainerClasses = []TrainerClass{
	// -- Kanto Gym Leaders (FRLG) --
	{Slug: "pex-leader_brock_frlg",    Label: "Brock (FRLG)",        SpriteURL: "/static/sprites/pex/leader_brock_frlg.png"},
	{Slug: "pex-leader_misty_frlg",    Label: "Misty (FRLG)",        SpriteURL: "/static/sprites/pex/leader_misty_frlg.png"},
	{Slug: "pex-leader_lt_surge_frlg", Label: "Lt. Surge (FRLG)",    SpriteURL: "/static/sprites/pex/leader_lt_surge_frlg.png"},
	{Slug: "pex-leader_erika_frlg",    Label: "Erika (FRLG)",        SpriteURL: "/static/sprites/pex/leader_erika_frlg.png"},
	{Slug: "pex-leader_koga_frlg",     Label: "Koga (FRLG)",         SpriteURL: "/static/sprites/pex/leader_koga_frlg.png"},
	{Slug: "pex-leader_sabrina_frlg",  Label: "Sabrina (FRLG)",      SpriteURL: "/static/sprites/pex/leader_sabrina_frlg.png"},
	{Slug: "pex-leader_blaine_frlg",   Label: "Blaine (FRLG)",       SpriteURL: "/static/sprites/pex/leader_blaine_frlg.png"},
	{Slug: "pex-leader_giovanni_frlg", Label: "Giovanni (FRLG)",     SpriteURL: "/static/sprites/pex/leader_giovanni_frlg.png"},

	// -- Kanto Elite Four & Champions (FRLG) --
	{Slug: "pex-elite_four_lorelei_frlg", Label: "Lorelei (FRLG)",      SpriteURL: "/static/sprites/pex/elite_four_lorelei_frlg.png"},
	{Slug: "pex-elite_four_bruno_frlg",   Label: "Bruno (FRLG)",         SpriteURL: "/static/sprites/pex/elite_four_bruno_frlg.png"},
	{Slug: "pex-elite_four_agatha_frlg",  Label: "Agatha (FRLG)",        SpriteURL: "/static/sprites/pex/elite_four_agatha_frlg.png"},
	{Slug: "pex-elite_four_lance_frlg",   Label: "Lance (FRLG)",         SpriteURL: "/static/sprites/pex/elite_four_lance_frlg.png"},
	{Slug: "pex-champion_steven_frlg",    Label: "Steven (FRLG)",        SpriteURL: "/static/sprites/pex/champion_steven_frlg.png"},
	{Slug: "pex-champion_rival_frlg",     Label: "Blue (FRLG)",          SpriteURL: "/static/sprites/pex/champion_rival_frlg.png"},
	{Slug: "pex-rival_early_frlg",        Label: "Blue Early (FRLG)",    SpriteURL: "/static/sprites/pex/rival_early_frlg.png"},
	{Slug: "pex-rival_late_frlg",         Label: "Blue Late (FRLG)",     SpriteURL: "/static/sprites/pex/rival_late_frlg.png"},

	// -- Professors & Scientists (FRLG) --
	{Slug: "pex-professor_oak_frlg", Label: "Prof. Oak (FRLG)",    SpriteURL: "/static/sprites/pex/professor_oak_frlg.png"},
	{Slug: "pex-scientist_frlg",     Label: "Scientist (FRLG)",    SpriteURL: "/static/sprites/pex/scientist_frlg.png"},
	{Slug: "pex-engineer_frlg",      Label: "Engineer (FRLG)",     SpriteURL: "/static/sprites/pex/engineer_frlg.png"},

	// -- Team Rocket (FRLG) --
	{Slug: "pex-rocket_grunt_f_frlg", Label: "Rocket Grunt F (FRLG)", SpriteURL: "/static/sprites/pex/rocket_grunt_f_frlg.png"},
	{Slug: "pex-rocket_grunt_m_frlg", Label: "Rocket Grunt M (FRLG)", SpriteURL: "/static/sprites/pex/rocket_grunt_m_frlg.png"},

	// -- Trainer Classes: New to FRLG --
	{Slug: "pex-burglar_frlg",    Label: "Burglar (FRLG)",     SpriteURL: "/static/sprites/pex/burglar_frlg.png"},
	{Slug: "pex-biker_frlg",      Label: "Biker (FRLG)",       SpriteURL: "/static/sprites/pex/biker_frlg.png"},
	{Slug: "pex-juggler_frlg",    Label: "Juggler (FRLG)",     SpriteURL: "/static/sprites/pex/juggler_frlg.png"},
	{Slug: "pex-channeler_frlg",  Label: "Channeler (FRLG)",   SpriteURL: "/static/sprites/pex/channeler_frlg.png"},
	{Slug: "pex-cue_ball_frlg",   Label: "Cue Ball (FRLG)",    SpriteURL: "/static/sprites/pex/cue_ball_frlg.png"},
	{Slug: "pex-gamer_frlg",      Label: "Gamer (FRLG)",       SpriteURL: "/static/sprites/pex/gamer_frlg.png"},
	{Slug: "pex-painter_frlg",    Label: "Painter (FRLG)",     SpriteURL: "/static/sprites/pex/painter_frlg.png"},
	{Slug: "pex-rocker_frlg",     Label: "Rocker (FRLG)",      SpriteURL: "/static/sprites/pex/rocker_frlg.png"},
	{Slug: "pex-tamer_frlg",      Label: "Tamer (FRLG)",       SpriteURL: "/static/sprites/pex/tamer_frlg.png"},
	{Slug: "pex-crush_girl_frlg", Label: "Crush Girl (FRLG)",  SpriteURL: "/static/sprites/pex/crush_girl_frlg.png"},
	{Slug: "pex-crush_kin_frlg",  Label: "Crush Kin (FRLG)",   SpriteURL: "/static/sprites/pex/crush_kin_frlg.png"},
	{Slug: "pex-super_nerd_frlg", Label: "Super Nerd (FRLG)",  SpriteURL: "/static/sprites/pex/super_nerd_frlg.png"},

	// -- Trainer Classes: FRLG variants of existing classes --
	{Slug: "pex-aroma_lady_frlg",           Label: "Aroma Lady (FRLG)",            SpriteURL: "/static/sprites/pex/aroma_lady_frlg.png"},
	{Slug: "pex-beauty_frlg",               Label: "Beauty (FRLG)",                SpriteURL: "/static/sprites/pex/beauty_frlg.png"},
	{Slug: "pex-bird_keeper_frlg",          Label: "Bird Keeper (FRLG)",           SpriteURL: "/static/sprites/pex/bird_keeper_frlg.png"},
	{Slug: "pex-black_belt_frlg",           Label: "Black Belt (FRLG)",            SpriteURL: "/static/sprites/pex/black_belt_frlg.png"},
	{Slug: "pex-bug_catcher_frlg",          Label: "Bug Catcher (FRLG)",           SpriteURL: "/static/sprites/pex/bug_catcher_frlg.png"},
	{Slug: "pex-camper_frlg",               Label: "Camper (FRLG)",                SpriteURL: "/static/sprites/pex/camper_frlg.png"},
	{Slug: "pex-collector_frlg",            Label: "Collector (FRLG)",             SpriteURL: "/static/sprites/pex/collector_frlg.png"},
	{Slug: "pex-cool_couple_frlg",          Label: "Cool Couple (FRLG)",           SpriteURL: "/static/sprites/pex/cool_couple_frlg.png"},
	{Slug: "pex-cool_trainer_f_frlg",       Label: "Cool Trainer F (FRLG)",        SpriteURL: "/static/sprites/pex/cool_trainer_f_frlg.png"},
	{Slug: "pex-cool_trainer_m_frlg",       Label: "Cool Trainer M (FRLG)",        SpriteURL: "/static/sprites/pex/cool_trainer_m_frlg.png"},
	{Slug: "pex-cycling_triathlete_f_frlg", Label: "Cycling Triathlete F (FRLG)",  SpriteURL: "/static/sprites/pex/cycling_triathlete_f_frlg.png"},
	{Slug: "pex-cycling_triathlete_m_frlg", Label: "Cycling Triathlete M (FRLG)",  SpriteURL: "/static/sprites/pex/cycling_triathlete_m_frlg.png"},
	{Slug: "pex-dragon_tamer_frlg",         Label: "Dragon Tamer (FRLG)",          SpriteURL: "/static/sprites/pex/dragon_tamer_frlg.png"},
	{Slug: "pex-expert_f_frlg",             Label: "Expert F (FRLG)",              SpriteURL: "/static/sprites/pex/expert_f_frlg.png"},
	{Slug: "pex-expert_m_frlg",             Label: "Expert M (FRLG)",              SpriteURL: "/static/sprites/pex/expert_m_frlg.png"},
	{Slug: "pex-fisherman_frlg",            Label: "Fisherman (FRLG)",             SpriteURL: "/static/sprites/pex/fisherman_frlg.png"},
	{Slug: "pex-gentleman_frlg",            Label: "Gentleman (FRLG)",             SpriteURL: "/static/sprites/pex/gentleman_frlg.png"},
	{Slug: "pex-guitarist_frlg",            Label: "Guitarist (FRLG)",             SpriteURL: "/static/sprites/pex/guitarist_frlg.png"},
	{Slug: "pex-hex_maniac_frlg",           Label: "Hex Maniac (FRLG)",            SpriteURL: "/static/sprites/pex/hex_maniac_frlg.png"},
	{Slug: "pex-hiker_frlg",               Label: "Hiker (FRLG)",                 SpriteURL: "/static/sprites/pex/hiker_frlg.png"},
	{Slug: "pex-interviewer_frlg",          Label: "Interviewer (FRLG)",           SpriteURL: "/static/sprites/pex/interviewer_frlg.png"},
	{Slug: "pex-kindler_frlg",              Label: "Kindler (FRLG)",               SpriteURL: "/static/sprites/pex/kindler_frlg.png"},
	{Slug: "pex-lady_frlg",                Label: "Lady (FRLG)",                  SpriteURL: "/static/sprites/pex/lady_frlg.png"},
	{Slug: "pex-lass_frlg",                Label: "Lass (FRLG)",                  SpriteURL: "/static/sprites/pex/lass_frlg.png"},
	{Slug: "pex-parasol_lady_frlg",         Label: "Parasol Lady (FRLG)",          SpriteURL: "/static/sprites/pex/parasol_lady_frlg.png"},
	{Slug: "pex-picnicker_frlg",            Label: "Picnicker (FRLG)",             SpriteURL: "/static/sprites/pex/picnicker_frlg.png"},
	{Slug: "pex-pokefan_f_frlg",            Label: "Pokefan F (FRLG)",             SpriteURL: "/static/sprites/pex/pokefan_f_frlg.png"},
	{Slug: "pex-pokefan_m_frlg",            Label: "Pokefan M (FRLG)",             SpriteURL: "/static/sprites/pex/pokefan_m_frlg.png"},
	{Slug: "pex-pokemaniac_frlg",           Label: "Pokemaniac (FRLG)",            SpriteURL: "/static/sprites/pex/pokemaniac_frlg.png"},
	{Slug: "pex-pokemon_breeder_frlg",      Label: "Pokemon Breeder (FRLG)",       SpriteURL: "/static/sprites/pex/pokemon_breeder_frlg.png"},
	{Slug: "pex-pokemon_ranger_f_frlg",     Label: "Pokemon Ranger F (FRLG)",      SpriteURL: "/static/sprites/pex/pokemon_ranger_f_frlg.png"},
	{Slug: "pex-pokemon_ranger_m_frlg",     Label: "Pokemon Ranger M (FRLG)",      SpriteURL: "/static/sprites/pex/pokemon_ranger_m_frlg.png"},
	{Slug: "pex-psychic_f_frlg",            Label: "Psychic F (FRLG)",             SpriteURL: "/static/sprites/pex/psychic_f_frlg.png"},
	{Slug: "pex-psychic_m_frlg",            Label: "Psychic M (FRLG)",             SpriteURL: "/static/sprites/pex/psychic_m_frlg.png"},
	{Slug: "pex-rich_boy_frlg",             Label: "Rich Boy (FRLG)",              SpriteURL: "/static/sprites/pex/rich_boy_frlg.png"},
	{Slug: "pex-ruin_maniac_frlg",          Label: "Ruin Maniac (FRLG)",           SpriteURL: "/static/sprites/pex/ruin_maniac_frlg.png"},
	{Slug: "pex-running_triathlete_f_frlg", Label: "Running Triathlete F (FRLG)",  SpriteURL: "/static/sprites/pex/running_triathlete_f_frlg.png"},
	{Slug: "pex-running_triathlete_m_frlg", Label: "Running Triathlete M (FRLG)",  SpriteURL: "/static/sprites/pex/running_triathlete_m_frlg.png"},
	{Slug: "pex-sailor_frlg",               Label: "Sailor (FRLG)",                SpriteURL: "/static/sprites/pex/sailor_frlg.png"},
	{Slug: "pex-school_kid_f_frlg",         Label: "School Kid F (FRLG)",          SpriteURL: "/static/sprites/pex/school_kid_f_frlg.png"},
	{Slug: "pex-school_kid_m_frlg",         Label: "School Kid M (FRLG)",          SpriteURL: "/static/sprites/pex/school_kid_m_frlg.png"},
	{Slug: "pex-sis_and_bro_frlg",          Label: "Sis & Bro (FRLG)",             SpriteURL: "/static/sprites/pex/sis_and_bro_frlg.png"},
	{Slug: "pex-sr_and_jr_frlg",            Label: "Sr. & Jr. (FRLG)",             SpriteURL: "/static/sprites/pex/sr_and_jr_frlg.png"},
	{Slug: "pex-swimmer_f_frlg",            Label: "Swimmer F (FRLG)",             SpriteURL: "/static/sprites/pex/swimmer_f_frlg.png"},
	{Slug: "pex-swimmer_m_frlg",            Label: "Swimmer M (FRLG)",             SpriteURL: "/static/sprites/pex/swimmer_m_frlg.png"},
	{Slug: "pex-swimming_triathlete_f_frlg",Label: "Swimming Triathlete F (FRLG)", SpriteURL: "/static/sprites/pex/swimming_triathlete_f_frlg.png"},
	{Slug: "pex-swimming_triathlete_m_frlg",Label: "Swimming Triathlete M (FRLG)", SpriteURL: "/static/sprites/pex/swimming_triathlete_m_frlg.png"},
	{Slug: "pex-tuber_f_frlg",              Label: "Tuber F (FRLG)",               SpriteURL: "/static/sprites/pex/tuber_f_frlg.png"},
	{Slug: "pex-tuber_m_frlg",              Label: "Tuber M (FRLG)",               SpriteURL: "/static/sprites/pex/tuber_m_frlg.png"},
	{Slug: "pex-twins_frlg",                Label: "Twins (FRLG)",                 SpriteURL: "/static/sprites/pex/twins_frlg.png"},
	{Slug: "pex-wally_frlg",                Label: "Wally (FRLG)",                 SpriteURL: "/static/sprites/pex/wally_frlg.png"},
	{Slug: "pex-young_couple_frlg",         Label: "Young Couple (FRLG)",          SpriteURL: "/static/sprites/pex/young_couple_frlg.png"},
	{Slug: "pex-youngster_frlg",            Label: "Youngster (FRLG)",             SpriteURL: "/static/sprites/pex/youngster_frlg.png"},
}

// pokemon-go-api (github.com/pokemon-go-api/pokemon-go-api) aggregates LeekDuck raid
// bosses and snacknap Max Battles into clean JSON, refreshed regularly. Both the
// raidboss and maxbattles endpoints share this entry shape under "currentList".
type pgapiList struct {
	CurrentList json.RawMessage `json:"currentList"`
}

type pgapiBoss struct {
	Names struct {
		English string `json:"English"`
	} `json:"names"`
	Assets struct {
		Image string `json:"image"`
	} `json:"assets"`
	Shiny        bool     `json:"shiny"`
	Types        []string `json:"types"`
	CPRange      []int    `json:"cpRange"`
	CPRangeBoost []int    `json:"cpRangeBoost"`
}

// bossFromPGAPI maps a pokemon-go-api entry onto our raidBoss shape. Shadow tiers carry
// the plain species name, so prefix "Shadow " to preserve the prior display behavior.
func bossFromPGAPI(b pgapiBoss, shadow bool) raidBoss {
	name := b.Names.English
	if shadow {
		name = "Shadow " + name
	}
	rb := raidBoss{
		PokemonName: name,
		ImageURL:    b.Assets.Image,
		Types:       b.Types,
		CanBeShiny:  b.Shiny,
	}
	if len(b.CPRange) == 2 {
		rb.CP, rb.CPMax = b.CPRange[0], b.CPRange[1]
	}
	if len(b.CPRangeBoost) == 2 {
		rb.CPBoostedMin, rb.CPBoostedMax = b.CPRangeBoost[0], b.CPRangeBoost[1]
	}
	return rb
}

type raidBoss struct {
	PokemonName   string   `json:"pokemon_name"`
	CP            int      `json:"cp"`
	CPMax         int      `json:"cp_max,omitempty"`
	CPBoostedMin  int      `json:"cp_boosted_min,omitempty"`
	CPBoostedMax  int      `json:"cp_boosted_max,omitempty"`
	ImageURL      string   `json:"image_url,omitempty"`
	Types         []string `json:"types,omitempty"`
	CanBeShiny    bool     `json:"can_be_shiny,omitempty"`
}

type PokemonEntry struct {
	Name string
	ID   int
}

// pokemonNameEntry mirrors one entry in PoGoAPI's pokemon_names.json per-id array.
type pokemonNameEntry struct {
	LanguageID int    `json:"language_id"`
	Name       string `json:"name"`
}

// langIDToCode maps PokeAPI local_language_id values (also used by the legacy
// PoGoAPI payloads) to our supported locale codes.
var langIDToCode = map[int]string{
	5:  "fr",
	6:  "de",
	7:  "es",
	11: "ja",
}

// supportedLangCodes is the set of locale codes we accept in the cached
// pokemon_names.json payload, derived from langIDToCode.
var supportedLangCodes = func() map[string]bool {
	m := make(map[string]bool, len(langIDToCode))
	for _, code := range langIDToCode {
		m[code] = true
	}
	return m
}()

type Store struct {
	mu                sync.RWMutex
	raids             json.RawMessage
	maxBattles        json.RawMessage
	events            json.RawMessage
	eventDetails      map[string]eventDetail
	detailsRunning    atomic.Bool
	pokemon           json.RawMessage
	pokemonMoves      json.RawMessage
	fastMoves         json.RawMessage
	chargedMoves      json.RawMessage
	shinies           json.RawMessage
	shadowPokemon     json.RawMessage
	typeChart         json.RawMessage
	cpMults           json.RawMessage
	pokemonTypes      json.RawMessage
	evolutions        json.RawMessage
	pokemonIDMap      map[string]int
	pokemonNamesById  map[int]map[string]string // dex ID → {lang_code → translated name}
	trainerClasses    []TrainerClass
	spriteCache       sync.Map
	client            *http.Client
	cacheDir          string
}

func New() *Store {
	newSlicesLen := 0
	for _, s := range showdownNewSlices {
		newSlicesLen += len(s)
	}
	classes := make([]TrainerClass, 0, len(showdownModernClasses)+newSlicesLen+len(dreamstoneTrainerClasses)+len(pexTrainerClasses)+len(platinumTrainerClasses))
	for _, tc := range showdownModernClasses {
		classes = append(classes, TrainerClass{Slug: tc.Slug, Label: tc.Label, SpriteURL: "/api/trainer-sprite/" + tc.Slug, Group: "showdown-modern"})
	}
	for _, slice := range showdownNewSlices {
		for _, tc := range slice {
			classes = append(classes, TrainerClass{Slug: tc.Slug, Label: tc.Label, SpriteURL: "/api/trainer-sprite/" + tc.Slug, Group: tc.Group})
		}
	}
	for _, tc := range dreamstoneTrainerClasses {
		tc.Group = "dreamstone"
		classes = append(classes, tc)
	}
	for _, tc := range pexTrainerClasses {
		tc.Group = "pex"
		classes = append(classes, tc)
	}
	for _, tc := range platinumTrainerClasses {
		tc.Group = "platinum"
		classes = append(classes, tc)
	}
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = "cache"
	}
	os.MkdirAll(cacheDir, 0755)
	return &Store{
		client:         &http.Client{Timeout: 15 * time.Second},
		trainerClasses: classes,
		cacheDir:       cacheDir,
		eventDetails:   make(map[string]eventDetail),
	}
}

func (s *Store) cachedFetch(key, url string) (json.RawMessage, error) {
	data, err := s.fetch(url)
	if err != nil {
		return nil, err
	}
	os.WriteFile(filepath.Join(s.cacheDir, key+".json"), data, 0644)
	return data, nil
}

// loadFromCache reads any previously saved JSON blobs from disk into the store.
// Fast and synchronous — called before the HTTP server starts listening.
func (s *Store) loadFromCache() {
	keys := []string{"raids", "max_battles", "events", "pokemon", "pokemon_moves", "fast_moves", "charged_moves", "shinies", "shadow_pokemon", "type_chart", "cp_multipliers", "pokemon_types", "pokemon_evolutions", "pokemon_names"}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		data, err := os.ReadFile(filepath.Join(s.cacheDir, key+".json"))
		if err != nil {
			continue
		}
		log.Printf("pogodata: %s: loaded from disk cache", key)
		s.applyResult(key, json.RawMessage(data))
	}
	s.loadEventDetailsFromDisk()
}

// applyResult writes a single data blob into the in-memory store. Caller must hold s.mu.
func (s *Store) applyResult(key string, data json.RawMessage) {
	switch key {
	case "raids":
		s.raids = data
	case "max_battles":
		s.maxBattles = data
	case "events":
		s.events = data
	case "pokemon":
		s.pokemon = data
		var arr []struct {
			Name string `json:"pokemon_name"`
			ID   int    `json:"pokemon_id"`
		}
		if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
			idMap := make(map[string]int, len(arr))
			for _, e := range arr {
				idMap[e.Name] = e.ID
			}
			s.pokemonIDMap = idMap
		} else {
			var obj map[string]struct {
				ID int `json:"pokemon_id"`
			}
			if err2 := json.Unmarshal(data, &obj); err2 == nil && len(obj) > 0 {
				idMap := make(map[string]int, len(obj))
				for name, e := range obj {
					idMap[name] = e.ID
				}
				s.pokemonIDMap = idMap
			} else {
				log.Printf("pogodata: pokemonIDMap: could not parse pokemon data (arr=%v obj=%v)", err, err2)
			}
		}
	case "pokemon_moves":
		s.pokemonMoves = data
	case "fast_moves":
		s.fastMoves = data
	case "charged_moves":
		s.chargedMoves = data
	case "shinies":
		s.shinies = mergeShinySupplement(data)
	case "shadow_pokemon":
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			names := make([]string, 0, len(raw))
			for name := range raw {
				names = append(names, name)
			}
			if encoded, err := json.Marshal(names); err == nil {
				s.shadowPokemon = encoded
			}
		}
	case "type_chart":
		s.typeChart = data
	case "cp_multipliers":
		s.cpMults = data
	case "pokemon_evolutions":
		s.evolutions = data
	case "pokemon_types":
		var arr []struct {
			Form  string   `json:"form"`
			Name  string   `json:"pokemon_name"`
			Types []string `json:"type"`
		}
		if err := json.Unmarshal(data, &arr); err != nil {
			log.Printf("pogodata: pokemon_types: parse error: %v", err)
			break
		}
		typeMap := make(map[string][]string)
		for _, entry := range arr {
			if _, exists := typeMap[entry.Name]; !exists || entry.Form == "Normal" {
				typeMap[entry.Name] = entry.Types
			}
		}
		if encoded, err := json.Marshal(typeMap); err == nil {
			s.pokemonTypes = encoded
		}
	case "pokemon_names":
		// Top-level map keyed by Pokémon ID string. The primary shape is what
		// refreshPokemonNames writes from PokeAPI's species names CSV:
		//   {"1":{"fr":"Bulbizarre","de":"Bisasam","es":"Bulbasaur","ja":"フシギダネ"}, ...}
		// Legacy PoGoAPI per-ID shapes are still accepted so an old cached file parses:
		//   A: [{"language_id":5,"name":"Bulbizarre"}, ...]          (array of structs)
		//   B: {"5":{"language_id":5,"name":"Bulbizarre"}, ...}      (map of lang_id → struct)
		//   C: {"5":"Bulbizarre", "6":"Bisasam", ...}                (map of lang_id → string)
		//   D: {"language_id":5,"name":"Bulbizarre"}                 (single struct)
		// The primary shape and C share Go types; numeric inner keys mean C.
		var rawTop map[string]json.RawMessage
		if err := json.Unmarshal(data, &rawTop); err != nil {
			log.Printf("pogodata: pokemon_names: parse error: %v", err)
			break
		}
		nameMap := make(map[int]map[string]string, len(rawTop))
		loggedFormat := false
		for idStr, inner := range rawTop {
			var id int
			fmt.Sscanf(idStr, "%d", &id)
			langs := make(map[string]string)

			// Format A: array of structs
			var arr []pokemonNameEntry
			if json.Unmarshal(inner, &arr) == nil {
				if !loggedFormat { log.Printf("pogodata: pokemon_names: format=A (array of structs)"); loggedFormat = true }
				for _, e := range arr {
					if code, ok := langIDToCode[e.LanguageID]; ok {
						langs[code] = e.Name
					}
				}
			} else if inner[0] == '{' {
				// Format B: map of lang_id_str → struct
				var mEntry map[string]pokemonNameEntry
				if json.Unmarshal(inner, &mEntry) == nil {
					if !loggedFormat { log.Printf("pogodata: pokemon_names: format=B (map of structs)"); loggedFormat = true }
					for _, e := range mEntry {
						if code, ok := langIDToCode[e.LanguageID]; ok {
							langs[code] = e.Name
						}
					}
				} else {
					// Primary shape (lang code keys) or format C (numeric lang id keys).
					var mStr map[string]string
					if json.Unmarshal(inner, &mStr) == nil {
						if !loggedFormat { log.Printf("pogodata: pokemon_names: format=map of strings (lang codes or ids)"); loggedFormat = true }
						for k, name := range mStr {
							if langID, err := strconv.Atoi(k); err == nil {
								if code, ok := langIDToCode[langID]; ok {
									langs[code] = name
								}
							} else if supportedLangCodes[k] {
								langs[k] = name
							}
						}
					} else {
						// Format D: single struct
						var single pokemonNameEntry
						if json.Unmarshal(inner, &single) == nil {
							if !loggedFormat { log.Printf("pogodata: pokemon_names: format=D (single struct)"); loggedFormat = true }
							if code, ok := langIDToCode[single.LanguageID]; ok {
								langs[code] = single.Name
							}
						}
					}
				}
			}

			if len(langs) > 0 {
				nameMap[id] = langs
			}
		}
		if !loggedFormat {
			log.Printf("pogodata: pokemon_names: unknown format, sample: %s", string(data[:min(200, len(data))]))
		}
		if len(nameMap) == 0 && len(s.pokemonNamesById) > 0 {
			log.Printf("pogodata: pokemon_names: empty result, keeping existing %d entries", len(s.pokemonNamesById))
			break
		}
		s.pokemonNamesById = nameMap
	}
}

// loadFallback loads the JSON files embedded in the binary at compile time.
// This guarantees the store always has baseline data even with no network and no cache.
func (s *Store) loadFallback() {
	keyToFile := map[string]string{
		"pokemon":        "fallback/pokemon.json",
		"pokemon_moves":  "fallback/pokemon_moves.json",
		"fast_moves":     "fallback/fast_moves.json",
		"charged_moves":  "fallback/charged_moves.json",
		"shinies":        "fallback/shinies.json",
		"shadow_pokemon": "fallback/shadow_pokemon.json",
		"type_chart":     "fallback/type_chart.json",
		"cp_multipliers": "fallback/cp_multipliers.json",
		"pokemon_types":  "fallback/pokemon_types.json",
		"pokemon_evolutions": "fallback/pokemon_evolutions.json",
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, path := range keyToFile {
		data, err := fallbackFS.ReadFile(path)
		if err != nil {
			log.Printf("pogodata: %s: embedded fallback missing: %v", key, err)
			continue
		}
		s.applyResult(key, json.RawMessage(data))
	}
	log.Println("pogodata: loaded embedded fallback data")
}

// shinySupplement holds locally curated shiny entries that the PogoAPI source is
// missing or has wrong (verified 2026-07-04: upstream lacks dex 813 to 818, the
// Scorbunny and Sobble lines, among others). Entries here always OVERRIDE upstream
// so corrections survive every refresh; delete an entry once upstream is confirmed
// correct. Merged in memory only: cache/shinies.json stays a pristine upstream
// mirror so CheckScrapers drift detection keeps working.
var shinySupplement = loadShinySupplement()

func loadShinySupplement() map[string]json.RawMessage {
	data, err := fallbackFS.ReadFile("fallback/shinies_supplement.json")
	if err != nil {
		log.Printf("pogodata: shinies supplement: %v", err)
		return nil
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // Go's JSON parser rejects a BOM
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		log.Printf("pogodata: shinies supplement: parse error: %v", err)
		return nil
	}
	return m
}

// mergeShinySupplement overlays the embedded supplement onto an upstream shinies
// blob, preserving the object-keyed-by-dex-string shape the frontend expects.
// On any parse failure the upstream blob is served unmerged.
func mergeShinySupplement(data json.RawMessage) json.RawMessage {
	if len(shinySupplement) == 0 {
		return data
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}), &m); err != nil {
		log.Printf("pogodata: shinies: parse error, serving upstream unmerged: %v", err)
		return data
	}
	if m == nil { // a literal JSON null unmarshals without error but leaves a nil map
		m = make(map[string]json.RawMessage, len(shinySupplement))
	}
	for k, v := range shinySupplement {
		m[k] = v
	}
	merged, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return merged
}

func (s *Store) Start() {
	s.loadFallback()    // always works: data is compiled into the binary
	s.loadFromCache()   // overlay with anything fresher saved to disk
	go s.refresh()          // try PoGoAPI in background
	go s.refreshRaids()     // try pokemon-go-api raids in background
	go s.refreshMaxBattles() // try pokemon-go-api max battles in background
	go s.refreshEvents()     // try ScrapedDuck events in background
	go s.refreshPokemonNames() // try PokeAPI localized names in background
	go func() {
		for range time.NewTicker(6 * time.Hour).C {
			s.refresh()
			s.refreshPokemonNames()
		}
	}()
	go func() {
		// ScrapedDuck asks for at least 5 minutes between fetches; 30 is plenty.
		for range time.NewTicker(30 * time.Minute).C {
			s.refreshEvents()
		}
	}()
	go s.scheduleRaidRefresh() // raids + max battles refresh on Mountain Time schedule
}

func (s *Store) refresh() {
	type ep struct {
		key  string
		urls []string
	}
	endpoints := []ep{
		{"pokemon",       []string{"https://pogoapi.net/api/v1/pokemon_stats.json"}},
		{"pokemon_moves", []string{"https://pogoapi.net/api/v1/current_pokemon_moves.json"}},
		{"fast_moves",    []string{"https://pogoapi.net/api/v1/fast_moves.json"}},
		{"charged_moves", []string{"https://pogoapi.net/api/v1/charged_moves.json"}},
		{"shinies",       []string{"https://pogoapi.net/api/v1/shiny_pokemon.json"}},
		{"shadow_pokemon", []string{"https://pogoapi.net/api/v1/shadow_pokemon.json"}},
		{"type_chart",    []string{"https://pogoapi.net/api/v1/type_effectiveness.json"}},
		{"cp_multipliers", []string{"https://pogoapi.net/api/v1/cp_multiplier.json"}},
		{"pokemon_types",  []string{"https://pogoapi.net/api/v1/pokemon_types.json"}},
		{"pokemon_evolutions", []string{"https://pogoapi.net/api/v1/pokemon_evolutions.json"}},
	}

	results := make(map[string]json.RawMessage, len(endpoints))

	// PoGoAPI endpoints: sequential with 400 ms delay to avoid rate limiting.
	for i, e := range endpoints {
		if i > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		var data json.RawMessage
		var lastErr error
		for _, url := range e.urls {
			data, lastErr = s.cachedFetch(e.key, url)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			log.Printf("pogodata: %s: all URLs failed", e.key)
		}
		if data != nil {
			results[e.key] = data
		}
	}

	s.mu.Lock()
	for key, data := range results {
		s.applyResult(key, data)
	}
	s.mu.Unlock()
	log.Println("pogodata: refresh complete")
}

// refreshRaids fetches current raid bosses from pokemon-go-api and writes to disk cache.
func (s *Store) refreshRaids() {
	data, err := s.fetchPGAPIRaids()
	if err != nil {
		log.Printf("pogodata: raids refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "raids.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("raids", data)
	s.mu.Unlock()
}

// refreshMaxBattles fetches current Max Battle bosses from pokemon-go-api and writes to
// disk cache.
func (s *Store) refreshMaxBattles() {
	data, err := s.fetchMaxBattles()
	if err != nil {
		log.Printf("pogodata: max battles refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "max_battles.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("max_battles", data)
	s.mu.Unlock()
}

// refreshEvents fetches current and upcoming events from ScrapedDuck (LeekDuck data)
// and writes to disk cache. On failure the last good payload is kept.
func (s *Store) refreshEvents() {
	data, err := s.cachedFetch("events", "https://raw.githubusercontent.com/bigfoott/ScrapedDuck/data/events.min.json")
	if err != nil {
		log.Printf("pogodata: events refresh: %v", err)
		return
	}
	s.mu.Lock()
	s.applyResult("events", data)
	s.mu.Unlock()
	log.Println("pogodata: events refresh complete")
	s.refreshEventDetails(data)
}

// refreshPokemonNames fetches localized species names from PokeAPI's CSV dataset,
// converts them to the JSON shape {"1":{"fr":"Bulbizarre",...},...}, and writes
// to disk cache. PoGoAPI's pokemon_names.json no longer carries language data,
// so this is the sole source of translated Pokémon names. On failure the last
// good payload is kept.
func (s *Store) refreshPokemonNames() {
	data, n, err := s.fetchPokemonNames()
	if err != nil {
		log.Printf("pogodata: pokemon names refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "pokemon_names.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("pokemon_names", data)
	s.mu.Unlock()
	log.Printf("pogodata: pokemon names: fetched %d species from PokeAPI", n)
}

// fetchPokemonNames fetches localized species names from PokeAPI's CSV dataset and
// returns them as the JSON shape {"1":{"fr":"Bulbizarre",...},...} plus the species
// count. It does not touch the store; callers persist/apply the result.
func (s *Store) fetchPokemonNames() (json.RawMessage, int, error) {
	const url = "https://raw.githubusercontent.com/PokeAPI/pokeapi/master/data/v2/csv/pokemon_species_names.csv"
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	names, err := parsePokemonNamesCSV(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("parse: %w", err)
	}
	if len(names) == 0 {
		return nil, 0, fmt.Errorf("no names parsed")
	}
	keyed := make(map[string]map[string]string, len(names))
	for id, langs := range names {
		keyed[strconv.Itoa(id)] = langs
	}
	data, err := json.Marshal(keyed)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal: %w", err)
	}
	return data, len(names), nil
}

// parsePokemonNamesCSV parses PokeAPI's pokemon_species_names.csv
// (pokemon_species_id,local_language_id,name,genus) into a map of dex ID to
// {lang_code: name} for the languages in langIDToCode. The header row and any
// row with a malformed id are skipped.
func parsePokemonNamesCSV(r io.Reader) (map[int]map[string]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	out := make(map[int]map[string]string)
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) < 3 {
			continue
		}
		id, errID := strconv.Atoi(rec[0])
		langID, errLang := strconv.Atoi(rec[1])
		if errID != nil || errLang != nil || id <= 0 || rec[2] == "" {
			continue
		}
		code, ok := langIDToCode[langID]
		if !ok {
			continue
		}
		if out[id] == nil {
			out[id] = make(map[string]string, len(langIDToCode))
		}
		out[id][code] = rec[2]
	}
	return out, nil
}

// scheduleRaidRefresh loops forever, sleeping until the next scheduled refresh
// time in Mountain Time (DST-aware via America/Denver), then fetching raids.
// Schedule: 12:01 AM, 4:00 AM, 8:00 AM, 12:01 PM, 4:00 PM, 8:00 PM.
func (s *Store) scheduleRaidRefresh() {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		log.Printf("pogodata: scheduleRaidRefresh: %v", err)
		return
	}
	type target struct{ h, m int }
	targets := []target{{0, 1}, {4, 0}, {8, 0}, {12, 1}, {16, 0}, {20, 0}}
	for {
		now := time.Now().In(loc)
		y, mo, d := now.Date()
		var next time.Time
		for _, t := range targets {
			candidate := time.Date(y, mo, d, t.h, t.m, 0, 0, loc)
			if now.Before(candidate) {
				next = candidate
				break
			}
		}
		if next.IsZero() {
			next = time.Date(y, mo, d+1, targets[0].h, targets[0].m, 0, 0, loc)
		}
		log.Printf("pogodata: raids: next scheduled refresh at %s", next.Format("2006-01-02 15:04 MST"))
		time.Sleep(time.Until(next))
		s.refreshRaids()
		s.refreshMaxBattles()
	}
}

// fetchPGAPIRaids fetches current raid bosses from pokemon-go-api and groups them by our
// numeric tier key (6=Mega, 5/3/1). Shadow tiers fold into their numeric tier with a
// "Shadow " name prefix.
func (s *Store) fetchPGAPIRaids() (json.RawMessage, error) {
	const url = "https://pokemon-go-api.github.io/pokemon-go-api/api/raidboss.json"
	tierMap := []struct {
		src    string
		dst    string
		shadow bool
	}{
		{"mega", "6", false},
		{"ultra_beast", "5", false},
		{"lvl5", "5", false},
		{"lvl3", "3", false},
		{"lvl1", "1", false},
		{"shadow_lvl5", "5", true},
		{"shadow_lvl3", "3", true},
		{"shadow_lvl1", "1", true},
	}
	grouped, total, err := s.fetchPGAPIGrouped(url, tierMap)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(grouped)
	if err != nil {
		return nil, err
	}
	log.Printf("pogodata: raids: fetched %d bosses from pokemon-go-api", total)
	return out, nil
}

// fetchMaxBattles fetches current Max Battle (Dynamax) bosses from pokemon-go-api, grouped
// by Max Battle tier (1/2/3).
func (s *Store) fetchMaxBattles() (json.RawMessage, error) {
	const url = "https://pokemon-go-api.github.io/pokemon-go-api/api/maxbattles.json"
	tierMap := []struct {
		src    string
		dst    string
		shadow bool
	}{
		{"tier_1", "1", false},
		{"tier_2", "2", false},
		{"tier_3", "3", false},
	}
	grouped, total, err := s.fetchPGAPIGrouped(url, tierMap)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(grouped)
	if err != nil {
		return nil, err
	}
	log.Printf("pogodata: max battles: fetched %d bosses from pokemon-go-api", total)
	return out, nil
}

// fetchPGAPIGrouped fetches a pokemon-go-api "currentList" endpoint and maps the named
// source tiers onto our destination tier keys, returning the grouped bosses and a count.
func (s *Store) fetchPGAPIGrouped(url string, tierMap []struct {
	src    string
	dst    string
	shadow bool
}) (map[string][]raidBoss, int, error) {
	raw, err := s.fetch(url)
	if err != nil {
		return nil, 0, err
	}
	var resp pgapiList
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, err
	}
	// currentList changed from a tier-keyed map to a flat array in the max battles endpoint.
	// If it's an array, return empty grouped data without error (correct when none are active).
	if cl := bytes.TrimSpace(resp.CurrentList); len(cl) > 0 && cl[0] == '[' {
		return make(map[string][]raidBoss), 0, nil
	}
	var currentList map[string]json.RawMessage
	if err := json.Unmarshal(resp.CurrentList, &currentList); err != nil {
		return nil, 0, err
	}
	grouped := make(map[string][]raidBoss)
	total := 0
	for _, m := range tierMap {
		blob, ok := currentList[m.src]
		if !ok {
			continue
		}
		var bosses []pgapiBoss
		if err := json.Unmarshal(blob, &bosses); err != nil {
			log.Printf("pogodata: %s parse @ %s: %v", m.src, url, err)
			continue
		}
		for _, b := range bosses {
			grouped[m.dst] = append(grouped[m.dst], bossFromPGAPI(b, m.shadow))
			total++
		}
	}
	if total == 0 {
		return nil, 0, fmt.Errorf("no bosses parsed from %s", url)
	}
	return grouped, total, nil
}

func (s *Store) fetch(url string) (json.RawMessage, error) {
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 120))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Refresh triggers an immediate data re-fetch outside the normal 6h schedule.
func (s *Store) Refresh() {
	go s.refresh()
	go s.refreshPokemonNames()
}

// ScraperCheck is the per-source result of CheckScrapers.
type ScraperCheck struct {
	Key        string `json:"key"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Count      int    `json:"count"`   // item count where meaningful (raids/max_battles), else 0
	Bytes      int    `json:"bytes"`   // size of the fetched payload
	Changed    bool   `json:"changed"` // fresh data differs from what was already stored
	DurationMs int64  `json:"duration_ms"`
}

// CheckScrapers synchronously fetches every scraped source fresh, compares it against
// what is currently cached, applies any changed data, and returns a per-source report.
// It fills the gap left by Refresh(), which skips raids/max_battles/events. Unlike the
// background refreshers it blocks until every source is fetched, so it is intended for a
// deliberate admin action rather than the normal schedule.
func (s *Store) CheckScrapers() []ScraperCheck {
	var out []ScraperCheck

	// run fetches one source, compares to the on-disk cache to detect drift, applies the
	// result on success, and records a ScraperCheck. after (optional) runs post-apply.
	run := func(key string, fetch func() (json.RawMessage, int, error), after func(json.RawMessage)) {
		start := time.Now()
		res := ScraperCheck{Key: key}
		data, count, err := fetch()
		res.DurationMs = time.Since(start).Milliseconds()
		if err != nil {
			res.Error = err.Error()
			out = append(out, res)
			return
		}
		old, _ := os.ReadFile(filepath.Join(s.cacheDir, key+".json"))
		res.Changed = !bytes.Equal(bytes.TrimSpace(old), bytes.TrimSpace(data))
		s.persistAndApply(key, data)
		if after != nil {
			after(data)
		}
		res.OK, res.Count, res.Bytes = true, count, len(data)
		out = append(out, res)
	}

	// Raids and max battles are shown first (the sources Refresh() cannot reach).
	run("raids", func() (json.RawMessage, int, error) {
		d, err := s.fetchPGAPIRaids()
		if err != nil {
			return nil, 0, err
		}
		return d, groupedCount(d), nil
	}, nil)
	run("max_battles", func() (json.RawMessage, int, error) {
		d, err := s.fetchMaxBattles()
		if err != nil {
			return nil, 0, err
		}
		return d, groupedCount(d), nil
	}, nil)
	run("events", func() (json.RawMessage, int, error) {
		d, err := s.fetch("https://raw.githubusercontent.com/bigfoott/ScrapedDuck/data/events.min.json")
		return d, 0, err
	}, func(d json.RawMessage) { s.refreshEventDetails(d) })
	run("pokemon_names", func() (json.RawMessage, int, error) { return s.fetchPokemonNames() }, nil)

	// PoGoAPI endpoints: sequential with a 400 ms delay to avoid rate limiting, matching refresh().
	pogoAPI := []struct{ key, url string }{
		{"pokemon", "https://pogoapi.net/api/v1/pokemon_stats.json"},
		{"pokemon_moves", "https://pogoapi.net/api/v1/current_pokemon_moves.json"},
		{"fast_moves", "https://pogoapi.net/api/v1/fast_moves.json"},
		{"charged_moves", "https://pogoapi.net/api/v1/charged_moves.json"},
		{"shinies", "https://pogoapi.net/api/v1/shiny_pokemon.json"},
		{"shadow_pokemon", "https://pogoapi.net/api/v1/shadow_pokemon.json"},
		{"type_chart", "https://pogoapi.net/api/v1/type_effectiveness.json"},
		{"cp_multipliers", "https://pogoapi.net/api/v1/cp_multiplier.json"},
		{"pokemon_types", "https://pogoapi.net/api/v1/pokemon_types.json"},
		{"pokemon_evolutions", "https://pogoapi.net/api/v1/pokemon_evolutions.json"},
	}
	for i, e := range pogoAPI {
		if i > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		url := e.url
		run(e.key, func() (json.RawMessage, int, error) {
			d, err := s.fetch(url)
			return d, 0, err
		}, nil)
	}
	return out
}

// persistAndApply writes a data blob to the disk cache and into the in-memory store,
// mirroring what the individual refresh functions do.
func (s *Store) persistAndApply(key string, data json.RawMessage) {
	os.WriteFile(filepath.Join(s.cacheDir, key+".json"), data, 0644)
	s.mu.Lock()
	s.applyResult(key, data)
	s.mu.Unlock()
}

// groupedCount sums the boss counts across tiers in a grouped raids/max_battles payload.
func groupedCount(data json.RawMessage) int {
	var m map[string][]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return 0
	}
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

func (s *Store) Raids() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.raids
}
func (s *Store) MaxBattles() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.maxBattles
}
func (s *Store) Events() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.events
}
func (s *Store) Pokemon() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemon
}
func (s *Store) PokemonMoves() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemonMoves
}
func (s *Store) Moves() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock()
	type combined struct {
		Fast    json.RawMessage `json:"fast"`
		Charged json.RawMessage `json:"charged"`
	}
	data, _ := json.Marshal(combined{Fast: s.fastMoves, Charged: s.chargedMoves})
	return data
}
func (s *Store) TrainerClasses() []TrainerClass {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.trainerClasses
}
func (s *Store) TrainerSpriteSourceURL(slug string) string { return showdownSources[slug] }
// PokemonDexID returns the national Pokédex ID for the given Pokémon name (0 if unknown).
func (s *Store) PokemonDexID(name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pokemonIDMap[name]
}

// PokemonList returns all known Pokémon sorted by name, for the settings autocomplete list.
func (s *Store) PokemonList() []PokemonEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PokemonEntry, 0, len(s.pokemonIDMap))
	for name, id := range s.pokemonIDMap {
		out = append(out, PokemonEntry{Name: name, ID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) PokemonTypes() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemonTypes
}
func (s *Store) CPMultipliers() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.cpMults
}
func (s *Store) Evolutions() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.evolutions
}
func (s *Store) SpriteCacheGet(slug string) ([]byte, bool) {
	v, ok := s.spriteCache.Load(slug)
	if !ok {
		return nil, false
	}
	return v.([]byte), true
}
func (s *Store) SpriteCacheSet(slug string, data []byte) { s.spriteCache.Store(slug, data) }
func (s *Store) AllData() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock()
	type all struct {
		Pokemon       json.RawMessage            `json:"pokemon"`
		PokemonMoves  json.RawMessage            `json:"pokemonMoves"`
		FastMoves     json.RawMessage            `json:"fastMoves"`
		ChargedMoves  json.RawMessage            `json:"chargedMoves"`
		Raids         json.RawMessage            `json:"raids"`
		MaxBattles    json.RawMessage            `json:"maxBattles"`
		Shinies       json.RawMessage            `json:"shinies"`
		ShadowPokemon json.RawMessage            `json:"shadowPokemon"`
		TypeChart     json.RawMessage            `json:"typeChart"`
		CPMultipliers json.RawMessage            `json:"cpMultipliers"`
		PokemonTypes  json.RawMessage            `json:"pokemonTypes"`
		PokemonNames  map[string]map[string]string `json:"pokemonNames,omitempty"`
	}
	// Build englishName → {lang_code → translated} from pokemonNamesById + pokemonIDMap.
	var pokemonNames map[string]map[string]string
	if len(s.pokemonNamesById) > 0 && len(s.pokemonIDMap) > 0 {
		pokemonNames = make(map[string]map[string]string, len(s.pokemonIDMap))
		for englishName, id := range s.pokemonIDMap {
			if langs, ok := s.pokemonNamesById[id]; ok && len(langs) > 0 {
				pokemonNames[englishName] = langs
			}
		}
	}
	data, _ := json.Marshal(all{
		Pokemon:       s.pokemon,
		PokemonMoves:  s.pokemonMoves,
		FastMoves:     s.fastMoves,
		ChargedMoves:  s.chargedMoves,
		Raids:         s.raids,
		MaxBattles:    s.maxBattles,
		Shinies:       s.shinies,
		ShadowPokemon: s.shadowPokemon,
		TypeChart:     s.typeChart,
		CPMultipliers: s.cpMults,
		PokemonTypes:  s.pokemonTypes,
		PokemonNames:  pokemonNames,
	})
	return data
}
