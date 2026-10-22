// SPDX-License-Identifier: GPL-2.0-only

package options

// Option objects of the front end's vocabulary. The option
// database holds one list of values per object; the printer tables use their
// own object numbers (see objectMap).
type Object int16

// Option objects.
const (
	ObjMedia           Object = 0
	ObjQuality         Object = 1
	ObjSmoothing       Object = 2
	ObjSupply          Object = 3
	ObjQualityLevel    Object = 4
	ObjResolutionV     Object = 5
	ObjResolutionH     Object = 6
	ObjEnhancedBlack   Object = 7
	ObjPaperSize       Object = 8
	ObjMessQuality     Object = 9
	ObjMessThickness   Object = 10
	ObjMessPaperLoad   Object = 11
	ObjMessResolution  Object = 12
	ObjMessSmoothing   Object = 13
	ObjDither          Object = 14
	ObjColourCorrect   Object = 15
	ObjBanner          Object = 16
	ObjCartridge       Object = 17
	ObjGrayscale       Object = 18
	ObjQualityMap1     Object = 19
	ObjQualityMap2     Object = 20
	ObjQualityMap3     Object = 21
	ObjAutoHTED        Object = 22
	ObjAutoHTPattern   Object = 23
	ObjGamma           Object = 24
	ObjMarginType      Object = 25
	ObjVivid           Object = 26
	ObjPaperGap        Object = 27
	ObjOverMargin      Object = 28
	ObjDuplex          Object = 29
	ObjInkSettings     Object = 30
	ObjIntent          Object = 31
	ObjDefaultMinus    Object = 32
	ObjCommandType     Object = 33
	ObjLowerPaperLimit Object = 34
	ObjBlackAdjust     Object = 35
)

// pairs is a list of (from, to) value pairs.
type pairs [][2]int16

func (p pairs) forward(v int16) (int16, bool) {
	for _, e := range p {
		if e[0] == v {
			return e[1], true
		}
	}
	return 0, false
}

func (p pairs) reverse(v int16) (int16, bool) {
	for _, e := range p {
		if e[1] == v {
			return e[0], true
		}
	}
	return 0, false
}

// objectMap translates a front-end object to the printer table's object id.
var objectMap = pairs{{0, 1}, {1, 3}, {2, 4}, {3, 5}, {4, 6}, {5, 7}, {6, 8}, {7, 9}, {8, 10}, {9, 11}, {10, 12},
	{11, 13}, {12, 14}, {13, 15}, {14, 17}, {15, 18}, {16, 19}, {17, 20}, {18, 21}, {19, 29}, {20, 30}, {21, 31},
	{22, 32}, {23, 33}, {24, 34}, {25, 35}, {26, 36}, {27, 39}, {28, 50}, {29, 38}, {30, 1030}, {31, 63}, {32, 67},
	{33, 72}, {34, 75}, {35, 64}}

// Value maps per object: (front-end value, table value).
var (
	bannerMap       = pairs{{0, 1}, {1, 2}, {2, 5}}
	cartridgeMap    = pairs{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 10}}
	ditherPatMap    = pairs{{0, 2560}, {1, 2816}, {2, 2562}, {3, 2595}, {4, 2600}}
	commandMap      = pairs{{0, 0}, {1, 1}, {2, 2}}
	duplexMap       = pairs{{0, 0}, {1, 2}}
	enhBlackMap     = pairs{{0, 0}, {1, 1}, {2, 10}}
	gammaMap        = pairs{{1, 10}, {2, 14}, {3, 18}, {4, 22}}
	grayscaleMap    = pairs{{0, 1}, {1, 0}, {2, 2}}
	intentMap       = pairs{{0, 1}, {1, 2}, {3, 4}}
	marginTypeMap   = pairs{{0, 0}, {1, 1}, {2, 10}}
	supplyMap       = pairs{{0, 4}, {1, 7}, {2, 20}, {3, 10}, {4, 11}, {5, 15}, {6, 16}, {7, 8}, {8, 28}, {12, 31}, {9, 29}, {10, 34}, {13, 36}, {14, 30}}
	mediaMap        = pairs{{0, 0}, {1, 1}, {2, 3}, {3, 4}, {4, 6}, {5, 7}, {6, 8}, {7, 15}, {8, 16}, {9, 18}, {10, 20}, {11, 21}, {12, 22}, {13, 24}, {14, 26}, {15, 27}, {16, 128}, {17, 30}}
	paperLoadMsgMap = pairs{{0, 0}, {1, 1}, {2, 2}, {4, 10}}
	qualityMsgMap   = pairs{{0, 0}, {1, 1}, {2, 5}, {3, 10}, {4, 15}, {5, 20}, {6, 25}, {7, 34}, {8, 35}, {9, 36}, {10, 40}, {11, 41}, {12, 42}, {13, 43}, {14, 44}, {15, 45}, {16, 30}}
	resolutionMap   = pairs{{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6}, {7, 7}, {8, 8}, {9, 9}, {10, 10}, {11, 11}}
	smoothLibMap    = pairs{{0, 0}, {1, 1}, {2, 3}, {3, 15}}
	thicknessMap    = pairs{{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6}, {7, 7}, {8, 8}, {9, 9}, {10, 10}, {11, 11}, {12, 20}}
	modelIDMap      = pairs{{46, 74}, {47, 73}, {57, 101}, {58, 102}, {59, 105}, {60, 108}}
	overMarginMap   = pairs{{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 10}}
	paperGapMap     = pairs{{0, 0}, {1, 1}, {2, 2}, {3, 10}}
	paperSizeMap    = pairs{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}, {7, 8}, {8, 9}, {9, 10}, {10, 11}, {11, 12}, {12, 13}, {13, 14}, {14, 15}, {15, 16}, {16, 17}, {17, 18}, {19, 20}, {20, 21}, {21, 22}, {22, 23}, {23, 24}, {24, 26}, {25, 28}, {26, 29}, {27, 30}, {28, 31}, {29, 32}, {30, 33}, {31, 34}, {32, 35}, {33, 40}, {34, 41}, {35, 42}, {36, 43}, {37, 44}, {38, 45}, {39, 256}, {40, 260}}
	printQualityMap = pairs{{-6, 0}, {-5, 5}, {-4, 10}, {-3, 15}, {-2, 20}, {-7, 30}}
	qualityLevelMap = pairs{{0, 0}, {1, 5}, {2, 10}, {3, 15}, {4, 25}, {5, 20}}
	smoothingMap    = pairs{{0, 1}, {1, 0}, {2, 2}, {3, 3}}
	vividMap        = pairs{{0, 1}, {1, 10}}
	inkTypeMap      = pairs{{0, 1001}, {1, 1002}, {2, 1003}, {3, 1004}, {4, 1005}, {5, 1006}, {6, 0x14}}
)

// cartridgeIDs maps (state word A, state word B) of the cartridge record to
// the cartridge id reported by ComSystem.
var cartridgeIDs = [][3]int16{{0, 10, 256}, {1, 11, 256}, {2, 12, 256}, {3, 28, 256}, {4, 2, 256}, {5, 3, 256},
	{6, 4, 256}, {7, 5, 256}, {8, 6, 256}, {9, 7, 256}, {10, 8, 256}, {11, 9, 256}, {12, 22, 256}, {13, 22, 23},
	{14, 23, 24}, {15, 26, 256}, {16, 27, 256}, {17, 25, 256}}

// formatTypes maps the table-format number to the front end's format type.
var formatTypes = pairs{{0, 1}, {2, 2}, {3, 3}, {4, 3}, {5, 3}, {6, 4}, {7, 4}, {8, 4}, {9, 5}, {10, 6}, {11, 6},
	{12, 7}, {13, 8}, {14, 8}, {15, 8}, {16, 9}, {17, 10}, {18, 11}}

// Format types the option rules compare against.
const (
	formatType4  = 4
	formatType6  = 6 // intent and contrast supported, balance_k not
	formatType8  = 8 // supply/size confirmation
	formatType9  = 9 // IVEC command type
	formatType10 = 10
	formatType11 = 11
)

// paperSizes lists (front-end paper size, width, height) in 1/100 mm.
var paperSizes = [][3]int64{{0, 14800, 21000}, {1, 21000, 14800}, {2, 21000, 29700}, {3, 29700, 21000},
	{4, 29700, 42000}, {5, 42000, 29700}, {6, 42000, 59400}, {7, 18200, 25700}, {8, 25700, 18200}, {9, 25700, 36400},
	{10, 36400, 25700}, {11, 36400, 51500}, {12, 21590, 27940}, {13, 27940, 21590}, {14, 21590, 35560},
	{15, 35560, 21590}, {16, 27940, 43180}, {17, 43180, 27940}, {18, 43180, 55800}, {19, 10000, 14800},
	{20, 14800, 10000}, {21, 24130, 10477}, {22, 22000, 11000}, {23, 34290, 43180}, {24, 21590, 27940},
	{25, 24130, 27940}, {26, 25400, 27940}, {27, 25400, 30480}, {28, 30480, 21590}, {29, 36830, 27940},
	{30, 38100, 27940}, {31, 38100, 30480}, {32, 37782, 27940}, {33, 23500, 10500}, {34, 19000, 9800},
	{35, 22860, 33782}, {36, 22350, 35560}, {37, 32900, 48300}, {38, 11860, 21390}, {39, 0, 0}, {40, 0, 0},
	{1046, 10477, 24130}, {1047, 11000, 22000}, {1048, 10500, 23500}, {1049, 9800, 19000}, {1050, 8900, 12700},
	{1051, 12700, 17800}, {1052, 10160, 15240}, {1054, 5500, 9100}, {1055, 12700, 17780}, {1056, 8900, 25400},
	{1057, 20000, 14800}, {1058, 12000, 23500}, {1059, 9000, 20500}, {1065, 5400, 8600}, {1066, 21000, 29700},
	{1069, 21590, 27940}, {1070, 10160, 20320}, {1073, 20320, 25400}, {1082, 10160, 18062}}

// valueMap returns the value map of an object and whether the object passes
// values through unchanged.
func valueMap(obj Object) (m pairs, passThrough bool) {
	switch obj {
	case ObjMedia, ObjDefaultMinus:
		return mediaMap, false
	case ObjQuality, ObjQualityMap1, ObjQualityMap2, ObjQualityMap3:
		return printQualityMap, false
	case ObjSmoothing:
		return smoothingMap, false
	case ObjSupply:
		return supplyMap, false
	case ObjQualityLevel:
		return qualityLevelMap, false
	case ObjResolutionV, ObjResolutionH, ObjAutoHTED, ObjAutoHTPattern:
		return nil, true
	case ObjEnhancedBlack:
		return enhBlackMap, false
	case ObjPaperSize, ObjLowerPaperLimit:
		return paperSizeMap, false
	case ObjMessQuality:
		return qualityMsgMap, false
	case ObjMessThickness:
		return thicknessMap, false
	case ObjMessPaperLoad:
		return paperLoadMsgMap, false
	case ObjMessResolution:
		return resolutionMap, false
	case ObjMessSmoothing:
		return smoothLibMap, false
	case ObjDither:
		return ditherPatMap, false
	case ObjColourCorrect:
		return bannerMap, false
	case ObjBanner:
		return bannerMap, false
	case ObjCartridge, ObjInkSettings:
		return cartridgeMap, false
	case ObjGrayscale:
		return grayscaleMap, false
	case ObjGamma:
		return gammaMap, false
	case ObjMarginType:
		return marginTypeMap, false
	case ObjVivid:
		return vividMap, false
	case ObjPaperGap:
		return paperGapMap, false
	case ObjOverMargin:
		return overMarginMap, false
	case ObjDuplex:
		return duplexMap, false
	case ObjIntent:
		return intentMap, false
	case ObjCommandType:
		return commandMap, false
	case ObjBlackAdjust:
		return pairs{}, false // known object without values
	}
	return nil, false
}
